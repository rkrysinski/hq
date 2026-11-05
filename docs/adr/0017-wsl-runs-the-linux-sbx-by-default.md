# 0017 - On WSL hq runs the Linux sbx by default

Extends the given of [ADR 0001](0001-tmux-and-sbx-are-givens.md) on Windows (#153). hq on WSL ran the Windows `sbx.exe` through interop, always. A team that installs only the Linux `sbx` inside WSL got `sbx not found` from every `hq new`, and worked around it with `HQ_PLATFORM=native`, a switch for tests (design §7.2) that also takes away what WSL gives: the taskbar flash when an agent needs the user, and `hq go` raising the window.

Now, on WSL, the `sbx` in `PATH` is the Linux sbx and hq runs it, giving it hq's own paths, without `wslpath`. Without one, hq runs `sbx.exe` as before; without either, it says that neither was found, naming both, and the installer refuses the same way. hq looks by name only: on WSL a Windows program keeps its `.exe`, so `sbx` is not checked for being a Linux program. Everything else of WSL stays: the bell, `hq go` through PowerShell, the browser through `explorer.exe`, Claude Desktop's file on the Windows side and its entry starting hq through `wsl.exe`.

The sbx side is a seam of its own, native or Windows through interop: the sbx command, the paths, what is said when it is missing, and the command line sbx's MCP gateway starts hq with. macOS and Linux always have the native side; WSL takes one by the rule above. So the gateway's command line follows where sbx runs, not the platform: with the Linux `sbx`, `hq mcp install sbx` registers `/usr/bin/env PATH=... hq mcp sbx`, which is what users of the workaround already have, so their next install leaves it alone.

## Considered Options

- **A switch that forces `sbx.exe`** when both are installed: no one asked for it; `PATH` already decides, and taking `sbx` off it gives `sbx.exe`.
- **Check that `sbx` is an ELF program**: guards against a case that does not happen on WSL, at the cost of reading the file at every start.
- **Migrate sandboxes between the two**: each `sbx` has its own daemon and its own sandboxes; hq does not move them.
- **Keep `HQ_PLATFORM=native` as the documented way**: it gives up the flash and the raise, and it is a test switch.

## Consequences

- With both installed, hq uses the Linux `sbx`; the sandboxes of `sbx.exe`, and the agents in them, are not visible to hq (README).
- The open question about repositories on the WSL file system (design §8) is closed: the Linux `sbx` mounts them as its own.
- `hq mcp install` on WSL says, while `HQ_PLATFORM=native` is set, that it is no longer needed and what to remove; nothing else says it, as the tests set the variable.
- `HQ_PLATFORM=wsl` stays the test switch for the Windows side, with `sbx.exe`, whatever `sbx` is in `PATH`.
