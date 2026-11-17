package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ranking"
)

const modelsUsage = "usage: decsr models rank [--json]"

func modelsCmd(args []string) {
	cwd, _ := os.Getwd()
	cfg, _ := config.Load(cwd)
	if err := runModels(args, os.Stdout, ranking.RunOptions{Settings: ranking.FromConfig(cfg)}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runModels runs `decsr models rank`: it fetches the rankings with the saved
// key, saves them and prints the table sorted by balance.
func runModels(args []string, out io.Writer, opts ranking.RunOptions) error {
	if len(args) == 0 || args[0] != "rank" {
		return errors.New(modelsUsage)
	}
	fs := flag.NewFlagSet("models rank", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return errors.New(modelsUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rep := ranking.Run(ctx, opts)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		for _, l := range rep.Lines() {
			_, _ = fmt.Fprintln(out, l)
		}
	}
	if rep.Err != nil {
		return errors.New("rankings not refreshed")
	}
	return nil
}
