// Command admin runs operator tasks against the database.
//
//	admin bootstrap --org-name NAME --email EMAIL
//
// creates a school organization, its first user and a school_admin membership. The password is
// read from the first line of stdin, never from a flag, so it stays out of shell history and the
// process list; on a terminal it is not echoed. It must have 10 to 128 characters.
//
//	admin reset-mfa --email EMAIL
//
// deletes the user's TOTP, recovery codes and every session (support path for a lost phone). The user
// signs in again with the password and, if their role requires it, enrolls a new authenticator.
// Verify the person's identity out of band before running it.
//
// DATABASE_URL must connect as a member of ce_app (the application role); both commands refuse to
// run otherwise (db.CheckAppRole).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/db"
)

const usage = "usage: admin bootstrap --org-name NAME --email EMAIL (password on stdin)\n" +
	"       admin reset-mfa --email EMAIL\n"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run returns the exit code: 0 success, 1 runtime failure, 2 usage error.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 && args[0] == "reset-mfa" {
		return resetMFA(ctx, args[1:], stdout, stderr, getenv)
	}
	if len(args) == 0 || args[0] != "bootstrap" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	orgName := fs.String("org-name", "", "name of the school")
	email := fs.String("email", "", "email of the school admin")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*orgName) == "" || !strings.Contains(*email, "@") || fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	fmt.Fprint(stderr, "Password: ")
	password, err := readPassword(stdin, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "admin: read password: %v\n", err)
		return 1
	}
	if auth.ValidatePassword(password) != nil {
		fmt.Fprintf(stderr, "admin: password must have %d to %d characters\n", auth.MinPasswordLen, auth.MaxPasswordLen)
		return 2
	}

	pool, code := connect(ctx, stderr, getenv)
	if pool == nil {
		return code
	}
	defer pool.Close()

	orgID, err := auth.NewStore(pool, time.Now).Bootstrap(ctx, strings.TrimSpace(*orgName), *email, password)
	if errors.Is(err, auth.ErrEmailTaken) {
		fmt.Fprintln(stderr, "admin: email already registered")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "admin: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "created organization %s with school admin %s\n", orgID, auth.NormalizeEmail(*email))
	return 0
}

// resetMFA runs `admin reset-mfa --email EMAIL`.
func resetMFA(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("reset-mfa", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "email of the account")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !strings.Contains(*email, "@") || fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	pool, code := connect(ctx, stderr, getenv)
	if pool == nil {
		return code
	}
	defer pool.Close()
	normalized, err := auth.NewStore(pool, time.Now).ResetMFA(ctx, *email)
	if errors.Is(err, auth.ErrNoAccount) {
		fmt.Fprintln(stderr, "admin: no account with that email")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "admin: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "reset two-factor authentication of %s and signed out its sessions\n", normalized)
	return 0
}

// connect opens DATABASE_URL and checks, before anything else, that its role is subject to
// Row-Level Security (db.CheckAppRole). On failure it reports to stderr and returns a nil pool and
// the exit code.
func connect(ctx context.Context, stderr io.Writer, getenv func(string) string) (*pgxpool.Pool, int) {
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(stderr, "admin: DATABASE_URL is not set")
		return nil, 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(stderr, "admin: connect: %v\n", err)
		return nil, 1
	}
	if err := db.CheckAppRole(ctx, pool); err != nil {
		pool.Close()
		fmt.Fprintf(stderr, "admin: %v\n", err)
		return nil, 1
	}
	return pool, 0
}

// readPassword reads one line from stdin without echo when stdin is a terminal, and as plain text
// otherwise (a pipe or a test).
func readPassword(stdin io.Reader, stderr io.Writer) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr) // the Enter key was not echoed
		return string(b), err
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
