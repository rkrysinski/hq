# 0001 - tmux and Docker Sandboxes are givens, not choices

Agents already run in `sbx` microVMs and live in tmux, surfaced as iTerm2 tabs on macOS and in Windows Terminal on WSL. hq is designed on top of that platform, not as a replacement for it. Consequence: state must leave the sandbox through the mounted repository, and sessions are addressed as tmux entities.
