import base64
import os
import pathlib
import re
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / '.github/workflows/ci.yml'
CONFIG = ROOT / '.gitleaks.toml'
GITLEAKS = os.environ.get('GITLEAKS')
TOKEN = 'ghp_' + 'aB5zQ9mT2wN7dK4vL8xR3sF6jH1cP0yE9uZq'
VECTORS = ('JBSWY3DP' + 'EHPK3PXP', base64.b32encode(b'12345678901234567890').decode())


@unittest.skipUnless(GITLEAKS, 'GITLEAKS binary is required')
class SecretScanTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)
        self.git('init')
        self.git('config', 'user.name', 'Test')
        self.git('config', 'user.email', 'test@example.invalid')
        self.git('commit', '--allow-empty', '-m', 'base')
        self.git('branch', 'base')
        self.git('checkout', '-b', 'pr')

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], stderr=subprocess.DEVNULL, text=True).strip()

    def commit(self, name, content, message):
        (self.root / name).write_text(content, encoding='utf-8')
        self.git('add', name)
        self.git('commit', '-m', message)

    def scan(self, base, config=False):
        options = re.search(r'--log-opts="([^"]+)"', WORKFLOW.read_text(encoding='utf-8')).group(1)
        options = options.replace('$BASE_SHA', base).replace('$HEAD_SHA', 'HEAD')
        command = [GITLEAKS, 'git', '--no-banner', '--redact', '--log-opts=' + options]
        if config:
            command += ['--config', str(CONFIG)]
        return subprocess.run(command + [str(self.root)], capture_output=True, text=True).returncode

    def test_clean_base_merge_is_not_rescanned(self):
        self.commit('pr.txt', 'clean\n', 'pr')
        self.git('checkout', 'base')
        self.commit('vector_test.go', 'secret = "' + VECTORS[0] + '"\n' + TOKEN + '\n', 'base vector')
        base = self.git('rev-parse', 'HEAD')
        self.git('checkout', 'pr')
        self.git('merge', '--no-ff', 'base', '-m', 'merge')
        self.assertEqual(self.scan(base), 0)

    def test_conflict_resolution_secret_fails_scan(self):
        self.commit('conflict.txt', 'author\n', 'pr')
        self.git('checkout', 'base')
        self.commit('conflict.txt', 'base\n', 'base')
        base = self.git('rev-parse', 'HEAD')
        self.git('checkout', 'pr')
        merge = subprocess.run(['git', '-C', str(self.root), 'merge', 'base'], capture_output=True)
        self.assertNotEqual(merge.returncode, 0)
        (self.root / 'conflict.txt').write_text('author\nbase\n' + TOKEN + '\n', encoding='utf-8')
        self.git('add', 'conflict.txt')
        self.git('commit', '-m', 'resolution')
        self.assertEqual(self.scan(base), 1)

    def test_known_vectors_are_allowed_only_in_test_paths(self):
        base = self.git('rev-parse', 'HEAD')
        for suffix in ('_test.go', '.test.ts', '.test.tsx', '.fixture.ts'):
            for i, vector in enumerate(VECTORS):
                self.commit('case' + str(i) + suffix, 'secret = "' + vector + '"\n', 'vector')
        self.assertEqual(self.scan(base, config=True), 0)
        for i, vector in enumerate(VECTORS):
            previous = self.git('rev-parse', 'HEAD')
            self.commit('production' + str(i) + '.go', 'secret = "' + vector + '"\n', 'production')
            self.assertEqual(self.scan(previous, config=True), 1)
        previous = self.git('rev-parse', 'HEAD')
        self.commit('other_test.go', TOKEN + '\n', 'other secret')
        self.assertEqual(self.scan(previous, config=True), 1)

    def test_normal_pr_commit_secret_fails_scan(self):
        base = self.git('rev-parse', 'HEAD')
        self.commit('production.txt', TOKEN + '\n', 'secret')
        self.assertEqual(self.scan(base), 1)


if __name__ == '__main__':
    unittest.main()
