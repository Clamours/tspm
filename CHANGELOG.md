# Changelog

## v0.1.0

- Initial Linux TUI (Bubble Tea) for multiple Task Spooler queues.
- Auto-discovery of conventional active listening tsp Unix sockets owned by the current user.
- Manual queue registration for arbitrary `TS_SOCKET` paths.
- Queue/task overview, task info, async output tailing (including gzip and carriage-return progress output), submit, kill, remove, clear, urgent, slots, manual refresh, and help overlay.
- CLI commands for queue listing, JSON status, diagnostics, queue registration, and task submission.
- Static Linux amd64/arm64 builds (`CGO_ENABLED=0`).
