// Command staff manages who may use the operations console, from the command line.
//
//	staff add you@playersense.dev --roles admin [--name "Ada Lovelace"]
//	staff list
//	staff deactivate someone@playersense.dev
//
// The console's own /v1/operator/staff routes do the same for an admin who is signed in. This exists
// for the first one: nobody can add an admin through the console until an admin exists, so the
// first is added by somebody with access to the database, and the row says so ("command line").
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"playersense.dev/backend/ent"
	"playersense.dev/backend/ent/staffmember"
	"playersense.dev/backend/internal/config"
	"playersense.dev/backend/internal/pgdb"
	"playersense.dev/backend/internal/staff"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "staff:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: staff add <email> --roles admin | staff list | staff deactivate <email>")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	settings, err := config.Load(config.EnvFile(root))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := pgdb.New(settings.PostgresDSN)
	if err := db.Start(ctx); err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		roles := fs.String("roles", "", "comma-separated: ops, support, billing, admin")
		name := fs.String("name", "", "their name")
		if len(args) < 2 {
			return errors.New("add needs an address")
		}
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		parsed, err := staff.Parse(strings.Split(*roles, ","))
		if err != nil {
			return err
		}
		row, err := db.Client.StaffMember.Create().
			SetEmail(staff.NormalizeEmail(args[1])).
			SetName(*name).
			SetRoles(staff.Names(parsed)).
			SetCreatedBy("command line").
			Save(ctx)
		if err != nil {
			if ent.IsConstraintError(err) {
				return fmt.Errorf("%s is already staff", args[1])
			}
			return err
		}
		fmt.Printf("added %s as %s\n", row.Email, strings.Join(row.Roles, ", "))
		return nil

	case "list":
		rows, err := db.Client.StaffMember.Query().Order(ent.Asc(staffmember.FieldEmail)).All(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			state := "active"
			if !r.Active {
				state = "deactivated"
			}
			fmt.Printf("%-40s %-28s %s\n", r.Email, strings.Join(r.Roles, ","), state)
		}
		return nil

	case "deactivate":
		if len(args) < 2 {
			return errors.New("deactivate needs an address")
		}
		n, err := db.Client.StaffMember.Update().
			Where(staffmember.Email(staff.NormalizeEmail(args[1]))).
			SetActive(false).SetDeactivatedAt(time.Now().UTC()).
			Save(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%s is not staff", args[1])
		}
		fmt.Printf("deactivated %s\n", args[1])
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}

// repoRoot walks upward looking for go.work, so the command works from anywhere in the tree.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.work not found above the working directory")
		}
		dir = parent
	}
}
