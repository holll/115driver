package cmd

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/SheltonZhu/115driver/cli/internal/auth"
	"github.com/SheltonZhu/115driver/cli/internal/output"
	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/spf13/cobra"
)

var (
	cookieFlag string
	configPath string
	profile    string
	jsonOutput bool
	debugMode  bool
)

// Set via -ldflags at build time
var version = "dev"

var newCLIClient = driver.New

var client *driver.Pan115Client
var printer *output.Printer

var rootCmd = &cobra.Command{
	Use:           "115driver",
	Short:         "CLI tool for 115 cloud storage",
	SilenceErrors: true,
	SilenceUsage:  true,
	Version:       version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		printer = output.NewPrinter(jsonOutput)

		name := cmd.Name()
		switch name {
		case "login", "help", "completion", "version", "config", "__complete", "__completeNoDesc":
			return nil
		}
		// Check parent for subcommands of config and completion
		if parent := cmd.Parent(); parent != nil {
			pname := parent.Name()
			if pname == "config" || pname == "completion" {
				return nil
			}
		}

		cr, err := auth.ResolveCredential(cookieFlag, configPath, profile)
		if err != nil {
			return &exitError{code: output.ExitAuth, msg: err.Error()}
		}

		opts := []driver.Option{driver.UA(driver.UA115Browser)}
		if debugMode {
			opts = append(opts, driver.WithDebug())
		}
		client = newCLIClient(opts...).ImportCredential(cr)

		if _, err := client.GetUser(); err != nil {
			var netErr net.Error
			var urlErr *url.Error
			if errors.As(err, &netErr) || errors.As(err, &urlErr) {
				return &exitError{code: output.ExitNetwork, msg: fmt.Sprintf("Network request failed: %v", err)}
			}
			return &exitError{code: output.ExitAuth, msg: fmt.Sprintf("Authentication failed: %v\nRun '115driver login' to re-authenticate.", err)}
		}
		return nil
	},
}

type exitError struct {
	code int
	msg  string
	data interface{}
}

func (e *exitError) Error() string {
	return e.msg
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cookieFlag, "cookie", "", "Cookie string (or set DRIVER115_COOKIE env)")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Config file path (default ~/.115driver/config.toml)")
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "Config profile name (default 'main')")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format (for AI agents)")
	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "Enable debug mode")
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
}

func Execute() int {
	// Pre-scan os.Args for --json so printer is initialized correctly
	// even when flag parsing fails early (unknown command, etc.)
	for _, arg := range os.Args[1:] {
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			jsonOutput = true
			break
		}
	}
	if printer == nil {
		printer = output.NewPrinter(jsonOutput)
	}

	if err := rootCmd.Execute(); err != nil {
		if ee, ok := err.(*exitError); ok {
			if ee.data != nil {
				return printer.PrintResult(ee.data, ee.msg, ee.code)
			}
			return printer.PrintError(ee.msg, ee.code)
		}
		return printer.PrintError(err.Error(), output.ExitError)
	}
	return output.ExitSuccess
}
