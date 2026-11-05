#!/usr/bin/env perl
# SessionStart hook: load <project_root>/state.md into the coordinator's
# model context only.
#
# state.md is the coordinator's handover record; worker panes
# (engineers, reviewers) must not see it. Reviewer instance B in
# particular relies on "fresh eyes" - leaking the coordinator's
# narrative about reviewer A's verdict defeats the second-pass design.
#
# Gate: load only when SPORE_TASK_SLUG is unset (interactive shell,
# operator-spawned session) or equals "coordinator" (the kernel sets
# this in the coordinator pane env). Worker spawns set
# SPORE_TASK_SLUG to the actual task slug.
#
# Resolves <project_root> via `git rev-parse --git-common-dir` so
# the same canonical state.md is read from any worktree under the
# project. Falls back to cwd when not in a git repo so the hook is
# harmless outside spore.
use strict;
use warnings;
use JSON::PP;
use Cwd qw(getcwd abs_path);

my $slug = $ENV{SPORE_TASK_SLUG};
exit 0 if defined $slug && $slug ne '' && $slug ne 'coordinator';

my $cwd = $ENV{CLAUDE_PROJECT_DIR} || getcwd();

my $root = $cwd;
my $gcd = `git -C "$cwd" rev-parse --git-common-dir 2>/dev/null`;
chomp $gcd;
if ($gcd ne '') {
    # rev-parse returns either ".git" (cwd is the toplevel) or an
    # absolute path under .worktrees/<slug>/.git. Walk to its parent
    # to land on the project root in either case.
    my $abs = ($gcd =~ m{^/}) ? $gcd : "$cwd/$gcd";
    my $resolved = abs_path("$abs/..");
    $root = $resolved if defined $resolved;
}

my $path = "$root/state.md";
my $bytes = -s $path;
open my $fh, '<:encoding(UTF-8)', $path or exit 0;
my $body = do { local $/; <$fh> };
close $fh;
exit 0 unless defined $body && length $body;

# Claude Code persists hook output above ~8KB to a file and injects
# only a short preview, so an oversized state.md is mostly lost.
my $budget = 8192;
if (defined $bytes && $bytes > $budget) {
    $body = "WARNING: state.md is $bytes bytes, over the $budget-byte "
          . "SessionStart budget; most of it is not injected. Run "
          . "`spore coordinator state ls`, then `spore coordinator state "
          . "archive <id>... --slug <name>` for verifiably done blocks.\n\n"
          . $body;
}

print encode_json({
  hookSpecificOutput => {
    hookEventName     => "SessionStart",
    additionalContext => $body,
  },
});
exit 0;
