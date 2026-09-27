# 0002 - no background daemon

Only the dashboard observes and refreshes; nothing runs when it is closed except the agents. Keeps installation to "put hq on PATH" and avoids a second thing that can be stale or dead. Cost: notifications for undocked agents exist only while the dashboard runs.
