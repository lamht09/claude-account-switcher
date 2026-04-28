package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/lamht09/claude-account-switcher/internal/app"
	"github.com/lamht09/claude-account-switcher/internal/output"
	"github.com/lamht09/claude-account-switcher/internal/updatecheck"
	"github.com/lamht09/claude-account-switcher/internal/updater"
)

var version = "dev"
var runUpdater = updater.Run

func main() {
	switcher := app.NewSwitcher()

	cfg, err := parseCLIArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", output.Red("CLI error:"), err)
		os.Exit(1)
	}
	if cfg.showVersion {
		fmt.Printf("ca %s\n", version)
		return
	}
	if cfg.showHelp {
		printHelp()
		return
	}

	if !cfg.skipRootCheck && isRoot() {
		fmt.Fprintln(os.Stderr, output.Yellow("Safety check: do not run this tool as root (except inside a container)."))
		os.Exit(1)
	}

	if err := runAction(cfg, switcher); err != nil {
		os.Exit(app.ToExitCode(err))
	}
	if !cfg.update {
		if msg := updatecheck.Check(version); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
	}
}

type cliConfig struct {
	addAccount    bool
	removeAccount string
	list          bool
	doSwitch      bool
	switchTo      string
	status        bool
	purge         bool
	repair        bool
	update        bool
	updateTo      string
	checkOnly     bool
	forceUpdate   bool
	tokenStatus   bool
	slot          int
	showVersion   bool
	showHelp      bool
	skipRootCheck bool
}

func parseCLIArgs(args []string) (cliConfig, error) {
	var cfg cliConfig
	fs := flag.NewFlagSet("ca", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.BoolVar(&cfg.showVersion, "version", false, "")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.showVersion {
		return cfg, nil
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		cfg.showHelp = true
		return cfg, nil
	}

	command := remaining[0]
	commandArgs := remaining[1:]

	switch command {
	case "add":
		addFS := flag.NewFlagSet("add", flag.ContinueOnError)
		addFS.SetOutput(os.Stderr)
		addFS.IntVar(&cfg.slot, "slot", 0, "")
		if err := addFS.Parse(commandArgs); err != nil {
			return cfg, err
		}
		if len(addFS.Args()) > 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for add: %v", addFS.Args())
		}
		if cfg.slot < 0 {
			return cfg, errors.New("--slot must be >= 0")
		}
		cfg.addAccount = true
	case "remove":
		if len(commandArgs) != 1 {
			return cfg, errors.New("remove requires exactly one identifier (slot number or email)")
		}
		cfg.removeAccount = commandArgs[0]
	case "list":
		listFS := flag.NewFlagSet("list", flag.ContinueOnError)
		listFS.SetOutput(os.Stderr)
		listFS.BoolVar(&cfg.tokenStatus, "token-status", false, "")
		if err := listFS.Parse(commandArgs); err != nil {
			return cfg, err
		}
		if len(listFS.Args()) > 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for list: %v", listFS.Args())
		}
		cfg.list = true
	case "switch":
		if len(commandArgs) != 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for switch: %v", commandArgs)
		}
		cfg.doSwitch = true
	case "switch-to":
		if len(commandArgs) != 1 {
			return cfg, errors.New("switch-to requires exactly one identifier (slot number or email)")
		}
		cfg.switchTo = commandArgs[0]
	case "status":
		if len(commandArgs) != 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for status: %v", commandArgs)
		}
		cfg.status = true
	case "purge":
		if len(commandArgs) != 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for purge: %v", commandArgs)
		}
		cfg.purge = true
	case "repair":
		if len(commandArgs) != 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for repair: %v", commandArgs)
		}
		cfg.repair = true
	case "update":
		updateFS := flag.NewFlagSet("update", flag.ContinueOnError)
		updateFS.SetOutput(os.Stderr)
		updateFS.StringVar(&cfg.updateTo, "to", "", "")
		updateFS.BoolVar(&cfg.checkOnly, "check-only", false, "")
		updateFS.BoolVar(&cfg.forceUpdate, "force", false, "")
		if err := updateFS.Parse(commandArgs); err != nil {
			return cfg, err
		}
		if len(updateFS.Args()) > 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for update: %v", updateFS.Args())
		}
		if strings.TrimSpace(cfg.updateTo) == "" && cfg.updateTo != "" {
			return cfg, errors.New("--to cannot be blank")
		}
		cfg.update = true
	case "help":
		if len(commandArgs) != 0 {
			return cfg, fmt.Errorf("unexpected positional arguments for help: %v", commandArgs)
		}
		cfg.showHelp = true
	default:
		return cfg, fmt.Errorf("unknown command: %s", command)
	}

	if cfg.showHelp {
		return cfg, nil
	}

	actionCount := 0
	for _, enabled := range []bool{
		cfg.addAccount,
		cfg.removeAccount != "",
		cfg.list,
		cfg.doSwitch,
		cfg.switchTo != "",
		cfg.status,
		cfg.purge,
		cfg.repair,
		cfg.update,
	} {
		if enabled {
			actionCount++
		}
	}
	if actionCount != 1 {
		return cfg, errors.New("exactly one action is required: add | remove | list | switch | switch-to | status | purge | repair | update")
	}
	if cfg.removeAccount != "" && !isNumericIdentifier(cfg.removeAccount) && !isValidEmail(cfg.removeAccount) {
		return cfg, fmt.Errorf("invalid email format: %s", cfg.removeAccount)
	}
	if cfg.switchTo != "" && !isNumericIdentifier(cfg.switchTo) && !isValidEmail(cfg.switchTo) {
		return cfg, fmt.Errorf("invalid email format: %s", cfg.switchTo)
	}
	cfg.skipRootCheck = runningInContainer()
	return cfg, nil
}

func runAction(cfg cliConfig, switcher *app.Switcher) error {
	switch {
	case cfg.addAccount:
		return switcher.Add(cfg.slot)
	case cfg.removeAccount != "":
		return switcher.Remove(cfg.removeAccount)
	case cfg.list:
		return switcher.List(cfg.tokenStatus)
	case cfg.doSwitch:
		return switcher.Switch()
	case cfg.switchTo != "":
		return switcher.SwitchTo(cfg.switchTo)
	case cfg.status:
		return switcher.Status()
	case cfg.purge:
		return switcher.Purge()
	case cfg.repair:
		return switcher.Repair()
	case cfg.update:
		return runUpdater(updater.Options{
			CurrentVersion: version,
			ToVersion:      cfg.updateTo,
			CheckOnly:      cfg.checkOnly,
			Force:          cfg.forceUpdate,
			Stdout:         os.Stdout,
			Stderr:         os.Stderr,
		})
	}
	return errors.New("no action selected")
}

func isRoot() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Geteuid() == 0
}

func runningInContainer() bool {
	if os.Getenv("CONTAINER") != "" || os.Getenv("container") != "" {
		return true
	}
	if runtime.GOOS == "windows" {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if fileContainsAny("/proc/1/cgroup", []string{"docker", "lxc", "containerd", "kubepods"}) {
		return true
	}
	if fileContainsAny("/proc/self/mountinfo", []string{"docker", "overlay"}) {
		return true
	}
	return false
}

var readFile = os.ReadFile
var emailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func fileContainsAny(path string, markers []string) bool {
	raw, err := readFile(path)
	if err != nil {
		return false
	}
	content := strings.ToLower(string(raw))
	for _, marker := range markers {
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}

func isNumericIdentifier(v string) bool {
	_, err := strconv.Atoi(v)
	return err == nil
}

func isValidEmail(v string) bool {
	return emailPattern.MatchString(v)
}

func printHelp() {
	fmt.Println("Usage: ca <command> [options]")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  add [--slot N]             Add current logged-in account")
	fmt.Println("  remove <slot|email>        Remove account by slot or email")
	fmt.Println("  list [--token-status]      List managed accounts")
	fmt.Println("  switch                     Switch to next account")
	fmt.Println("  switch-to <slot|email>     Switch to a specific account")
	fmt.Println("  status                     Show current live profile")
	fmt.Println("  purge                      Remove all managed backup data")
	fmt.Println("  repair                     Repair managed account metadata")
	fmt.Println("  update [--to V] [--check-only] [--force]  Check and self-update tool")
	fmt.Println("  help                       Show this help message")
	fmt.Println("")
	fmt.Println("Global flags:")
	fmt.Println("  --version                  Show tool version")
}
