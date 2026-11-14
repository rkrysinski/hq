# The project part of scripts/local-dev.sh: everything that knows how this
# project's app runs. scripts/local-dev.sh sources it (bash); change this file
# when the app changes and leave scripts/local-dev.sh as the template ships it.
#
# Not wired yet: `scripts/local-dev.sh start` fails until the four parts below
# describe this project's app (docs/agents/testing.md, "Wiring the checks").

# A path that answers 200 once the app is ready to be used.
READY_PATH=/health

# Text found in the command line of the app's process and of no other; `stop`
# refuses to kill a PID whose command line does not contain it.
APP_PROCESS_PATTERN="replace-with-the-apps-command-line"

# Installs what the app needs, with its output in the log. Must be safe to run
# again. Runs in the checkout root.
sync_app() {
    echo "local-dev: scripts/local-dev.project.sh is not wired for this project's stack yet" >&2
    return 1
}

# Runs before the app starts, with its output in the log: migrations, seed
# data, anything the app needs in $APP_DATA_DIR. Must be safe to run again.
prepare_app() {
    :
}

# Sets APP_COMMAND to the command that serves the app on port $1. It must stay
# in the foreground and must not reload itself.
app_command() {
    APP_COMMAND=(false)
}
