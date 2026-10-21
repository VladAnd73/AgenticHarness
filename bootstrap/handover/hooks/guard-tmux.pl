#!/usr/bin/env perl
# PreToolUse hook: keep agents off the operator's real tmux server for
# throwaway work. A bare `tmux kill-server` takes down every project
# session. A worker's scratch `tmux new-session` outlives the worker,
# and a name ending in /coordinator is taken for a real coordinator by
# spore-attach. Both must name a private socket with -L or -S.
use strict;
use warnings;
use JSON::PP;

my $raw = do { local $/; <STDIN> };
my $payload = eval { decode_json($raw) };
exit 0 unless ref($payload) eq 'HASH';
exit 0 unless ($payload->{tool_name} // '') eq 'Bash';
my $cmd = $payload->{tool_input}{command} // '';
my $worker = ($ENV{SPORE_TASK_SLUG} // '') ne '';

# A segment starts at a command separator or a quote (`bash -c "tmux
# ..."`). Before tmux it may carry env assignments, a wrapper (sudo, env,
# exec, command, nohup) and a path. tmux accepts any unique prefix of a
# subcommand, so `kill-ser` and `new-s` count too.
my $lead = qr{^\s*(?:\w+=\S*\s+)*(?:(?:sudo|env|exec|command|nohup)\s+(?:-\S+\s+)*(?:\w+=\S*\s+)*)*(?:\S*/)?|\$\(\s*|`\s*};
my $why;
for my $seg (split /(?:;|&&|\|\||\||\n|"|')/, $cmd) {
    next unless $seg =~ /(?:$lead)tmux((?:\s+-\w+(?:\s+(?!-)\S+)?)*?)\s+(kill-ser\w*|new-s\w*|new)(?:\s|$)/;
    my ($flags, $sub) = ($1, $2);
    next if $flags =~ /(?:^|\s)-[LS]/;
    if ($sub =~ /^kill/) {
        $why = "a bare `tmux kill-server` kills the operator's real server and every project session on it";
        last;
    }
    if ($worker) {
        $why = "a worker's `tmux $sub` on the real server outlives the task and can be mistaken for a project session";
        last;
    }
}
exit 0 unless defined $why;

print encode_json({
  hookSpecificOutput => {
    hookEventName            => "PreToolUse",
    permissionDecision       => "deny",
    permissionDecisionReason => "Denied: $why. Name a private server on both halves: "
        . "`tmux -L <label> new-session -d -s <name> ...` and `tmux -L <label> kill-server`. "
        . "To show the operator a process, use `tmux new-window -d -n <name> ...` instead.",
  },
});
exit 0;
