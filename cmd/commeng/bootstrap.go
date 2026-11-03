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

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

func bootstrapAdmin(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("bootstrap-admin", flag.ContinueOnError)
	name := flags.String("name", "", "administrator display name")
	email := flags.String("email", "", "administrator email")
	fromStdin := flags.Bool("password-stdin", false, "read password from stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*fromStdin {
		return errors.New("--password-stdin is required; never pass passwords as CLI arguments")
	}
	store, err := openStore(ctx, "postgres")
	if err != nil {
		return err
	}
	defer store.Close()
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && (password == "" || !errors.Is(err, io.EOF)) {
		return fmt.Errorf("read password: %w", err)
	}
	password = strings.TrimSuffix(password, "\n")
	password = strings.TrimSuffix(password, "\r")
	user, err := service.New(store, live.New(), "").BootstrapAdmin(ctx, *name, *email, password)
	if err != nil {
		return err
	}
	fmt.Printf("administrator created: id=%s email=%s\n", domain.ID(user.ID), user.Email)
	return nil
}
