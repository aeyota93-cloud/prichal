# shellcheck shell=sh
# git_setup makes git fail instead of asking questions on a terminal nobody
# has. Call it inside the checkout: a key set in the checkout's own
# core.sshCommand must win over the default below.
git_setup() {
  export GIT_TERMINAL_PROMPT=0
  git config core.sshCommand >/dev/null 2>&1 || export GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ConnectTimeout=15"
}

# Git runs as root here, and a checkout's .git/config can name commands for
# git to run. So git's own ownership check stays on (a checkout that is not
# root's is refused unless root added it to safe.directory), and fsmonitor,
# a speed-up the panel does not need, is off.
g() { git -c core.fsmonitor= "$@"; }
