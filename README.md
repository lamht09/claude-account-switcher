# claude-account-switcher

Multi-account switcher for Claude Code. Quickly switch between multiple Claude accounts on one machine without logging out each time.

Main command: `ca`

## Installation

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/lamht09/claude-account-switcher/main/install.sh | sh
```

### Windows PowerShell

```powershell
iwr https://raw.githubusercontent.com/lamht09/claude-account-switcher/main/install.ps1 -UseBasicParsing | iex
```

### Windows CMD

```bat
curl -fsSL -o install.cmd https://raw.githubusercontent.com/lamht09/claude-account-switcher/main/install.cmd
install.cmd
```

If `ca` is not found, open a new terminal and try again.

### Build from source

```bash
git clone https://github.com/lamht09/claude-account-switcher.git
cd claude-account-switcher
go build -o ca ./cmd/ca
./ca --version
```

## Usage

### Add your first account

Log into Claude Code with your first account, then:

```bash
ca add
```

### Add more accounts

Log in with another account, then:

```bash
ca add
```

### Switch accounts

Rotate to the next account:

```bash
ca switch
```

Or switch to a specific account:

```bash
ca switch-to 2
ca switch-to user@example.com
```

**Note:** Restart Claude Code (or close and reopen the VS Code extension tab) after switching for the new account to take effect.

### Repair managed account metadata

If you see identity drift or invalid slot metadata:

```bash
ca repair
```

### Other commands

```bash
ca list                        # Show all managed accounts
ca list --token-status         # Also show token health for each account
ca status                      # Show the current live profile
ca add --slot 3                # Save account to a specific slot (prompts before overwrite)
ca remove 2                    # Remove account by slot
ca remove user@example.com     # Remove account by email
ca purge                       # Remove all claude-account-switcher backup data
ca update                      # Check latest release and update if newer
ca update --check-only         # Check update status without modifying binary
ca update --to v1.2.3          # Update to a specific version
ca update --to v1.2.3 --force  # Reinstall target version even if same
ca help                        # Show command help
ca --version                   # Show tool version
```

`ca update` verifies release artifacts with `SHA256SUMS` before replacing the binary.

## Tips

- **Continue sessions after switching:** You can reopen Claude Code or the VS Code extension and continue previous sessions after running `ca switch` in any terminal.
- **Refresh an account:** If credentials expire, log into Claude Code with that account and run `ca add` again to update stored credentials.
- **First run shortcut:** `ca list` may offer to add your current logged-in account when no slot is managed yet.

## How it works

- Reads your live Claude identity from local Claude config
- Backs up account config and credentials when you run `ca add`
- Swaps live credentials/config when you run `ca switch` or `ca switch-to`
- Maintains slot metadata in `sequence.json` with lock protection to avoid concurrent write issues

## Common issues

- **No active account found:** Sign in to Claude Code first, then run `ca add`.
- **`ca` command not found:** Open a new terminal and try again.
- **Switched account but Claude Code still shows old account:** Close and reopen Claude Code (or the VS Code extension tab).
- **Tool exits when run as root:** Run as a normal user (root is blocked unless running inside a container).

## Uninstall

Remove all data:

```bash
ca purge
```

Then remove the installed binary:

- Linux/macOS: delete `~/.local/bin/ca` (or your custom `INSTALL_DIR`)
- Windows: delete `~/.local/bin/ca.exe` (or your custom `INSTALL_DIR`)

## Requirements

- Claude Code installed and logged in
- Go 1.24+ (only if building from source)

## Acknowledgements

This tool is a Go port of [`realiti4/claude-swap`](https://github.com/realiti4/claude-swap), based on version `v0.8.0`.

## License

MIT. See `LICENSE`.
