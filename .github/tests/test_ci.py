import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest

WORKFLOW = pathlib.Path(__file__).resolve().parents[1] / "workflows" / "ci.yml"


class WorkflowTests(unittest.TestCase):
    def test_only_pull_requests_cancel_previous_runs(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertRegex(workflow, r"(?m)^  cancel-in-progress: \$\{\{ github\.event_name == 'pull_request' \}\}$")
        self.assertRegex(workflow, r"(?m)^  pull_request:\s*$")

    def test_each_main_push_gets_its_own_group(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        group = re.search(r"(?m)^  group: (.+)$", workflow).group(1)

        def evaluate(context):
            def expression(match):
                python = re.sub(r"github\.([a-z_]+)", lambda m: repr(context[m.group(1)]), match.group(1))
                return str(eval(python.replace("&&", " and ").replace("||", " or ")))
            return re.sub(r"\$\{\{(.+?)\}\}", expression, group)

        main = lambda run_id: {"event_name": "push", "ref": "refs/heads/main", "run_id": run_id}
        pr = lambda run_id: {"event_name": "pull_request", "ref": "refs/pull/7/merge", "run_id": run_id}
        self.assertNotEqual(evaluate(main(1)), evaluate(main(2)))
        self.assertEqual(evaluate(pr(1)), evaluate(pr(2)))

    def test_all_jobs_use_ubuntu_24_04(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        runners = re.findall(r"(?m)^    runs-on: (.+)$", workflow)
        jobs = re.findall(r"(?m)^  [a-zA-Z][a-zA-Z0-9_-]*:\s*$", workflow.split("jobs:\n", 1)[1])
        self.assertTrue(jobs)
        self.assertEqual(len(jobs), len(runners))
        self.assertTrue(all(runner == "ubuntu-24.04" for runner in runners))


    def test_gofmt_job_rejects_unformatted_go(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("unformatted=$(gofmt -l .)\n          test -z \"$unformatted\"", workflow)
        root = WORKFLOW.parents[2]
        paths = [root / "api/internal/db/code_hash_test.go", root / "api/internal/httpapi/t7b_test.go"]
        self.assertEqual(subprocess.check_output(["gofmt", "-l", *map(str, paths)], text=True), "")
        with tempfile.TemporaryDirectory() as directory:
            bad = pathlib.Path(directory) / "bad.go"
            bad.write_text("package p\nfunc f( ){}\n", encoding="utf-8")
            self.assertNotEqual(subprocess.check_output(["gofmt", "-l", str(bad)], text=True), "")
            bash = shutil.which("bash")
            if pathlib.Path("C:/Program Files/Git/bin/bash.exe").exists():
                bash = "C:/Program Files/Git/bin/bash.exe"
            command = re.search(r"(?m)^          (unformatted=\$\(gofmt -l \.\))\n          (test -z .+)$", workflow)
            self.assertIsNotNone(command)
            self.assertNotEqual(subprocess.run([bash, "-e", "-c", "\n".join(command.groups())], cwd=directory).returncode, 0)

    def test_commit_lint_rejects_non_conventional_pr_commits(self):
        root = WORKFLOW.parents[2]
        script = root / ".github/scripts/check-commits.sh"
        self.assertTrue(script.is_file())
        bash = shutil.which("bash")
        if pathlib.Path("C:/Program Files/Git/bin/bash.exe").exists():
            bash = "C:/Program Files/Git/bin/bash.exe"
        with tempfile.TemporaryDirectory() as directory:
            def git(*args):
                return subprocess.check_output(["git", "-C", directory, *args], text=True, stderr=subprocess.DEVNULL).strip()
            git("init")
            git("config", "user.name", "Test")
            git("config", "user.email", "test@example.invalid")
            git("commit", "--allow-empty", "-m", "chore: base")
            base = git("rev-parse", "HEAD")
            git("commit", "--allow-empty", "-m", "fix(ci): valid change")
            self.assertEqual(subprocess.run([bash, str(script), base, "HEAD"], cwd=directory).returncode, 0)
            git("commit", "--allow-empty", "-m", "arreglos varios")
            self.assertNotEqual(subprocess.run([bash, str(script), base, "HEAD"], cwd=directory).returncode, 0)
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn(".github/scripts/check-commits.sh", workflow)

    def test_gitleaks_scans_the_pull_request_commit_range(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("gitleaks_8.30.1_linux_x64.tar.gz", workflow)
        self.assertIn("551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb", workflow)
        self.assertIn('gitleaks" git --redact --log-opts="-m $BASE_SHA..$HEAD_SHA"', workflow)
        with tempfile.TemporaryDirectory() as directory:
            def git(*args):
                return subprocess.check_output(["git", "-C", directory, *args], text=True, stderr=subprocess.DEVNULL).strip()
            git("init")
            git("config", "user.name", "Test")
            git("config", "user.email", "test@example.invalid")
            git("commit", "--allow-empty", "-m", "chore: base")
            base = git("rev-parse", "HEAD")
            git("checkout", "-b", "side")
            pathlib.Path(directory, "side.txt").write_text("side\n", encoding="utf-8")
            git("add", "side.txt")
            git("commit", "-m", "test: side")
            git("checkout", "-b", "primary", base)
            pathlib.Path(directory, "primary.txt").write_text("primary\n", encoding="utf-8")
            git("add", "primary.txt")
            git("commit", "-m", "test: primary")
            git("merge", "--no-ff", "--no-commit", "side")
            pathlib.Path(directory, "merge.txt").write_text("merge-only-marker\n", encoding="utf-8")
            git("add", "merge.txt")
            git("commit", "-m", "test: merge")
            self.assertIn("merge-only-marker", git("diff", base, "HEAD"))
            options = re.search(r'--log-opts="([^"]+)"', workflow).group(1)
            args = options.replace("$BASE_SHA", base).replace("$HEAD_SHA", "HEAD").split()
            self.assertIn("merge-only-marker", git("log", "-p", "-U0", *args))

    def test_ci_ok_fails_when_any_needed_job_is_not_successful(self):
        root = WORKFLOW.parents[2]
        script = root / ".github/scripts/ci-ok.py"
        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("if: always()", workflow)
        self.assertTrue(script.is_file())
        for result in ("success", "failure", "cancelled", "skipped"):
            env = dict(os.environ, NEEDS=json.dumps({"api": {"result": "success"}, "other": {"result": result}}))
            completed = subprocess.run(["python", str(script)], env=env)
            self.assertEqual(completed.returncode == 0, result == "success")
        jobs = re.findall(r"(?m)^  ([a-zA-Z][a-zA-Z0-9_-]*):\s*$", workflow.split("jobs:\n", 1)[1])
        needs = re.search(r"(?m)^    needs: \[(.+)\]$", workflow)
        self.assertIsNotNone(needs)
        self.assertEqual(set(needs.group(1).split(", ")), set(jobs) - {"ci-ok"})

    def test_existing_push_and_manual_entry_points_keep_api_and_web_jobs(self):
        workflow = WORKFLOW.read_text(encoding="utf-8")
        for entry in ("  push:", '    branches: ["**"]', "  workflow_dispatch:", "  api:", "  web:"):
            self.assertIn(entry, workflow)


if __name__ == "__main__":
    unittest.main()
