// Command admin runs operator tasks against the database.
//
//	admin bootstrap --org-name NAME --email EMAIL
//
// creates a school organization, its first user and a school_admin membership. The password is
// read from the first line of stdin, never from a flag, so it stays out of shell history and the
// process list. DATABASE_URL must connect as a member of ce_app (the application role).
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

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/auth"
)

const usage = "usage: admin bootstrap --org-name NAME --email EMAIL (password on stdin)\n"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run returns the exit code: 0 success, 1 runtime failure, 2 usage error.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
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

	// shortcut: the password echoes on an interactive terminal; pipe it in (or add golang.org/x/term).
	fmt.Fprint(stderr, "Password: ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(stderr, "admin: read password: %v\n", err)
		return 1
	}
	password := strings.TrimRight(line, "\r\n")
	if len([]rune(password)) < auth.MinPasswordLen {
		fmt.Fprintf(stderr, "admin: password must have at least %d characters\n", auth.MinPasswordLen)
		return 2
	}

	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(stderr, "admin: DATABASE_URL is not set")
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(stderr, "admin: connect: %v\n", err)
		return 1
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
