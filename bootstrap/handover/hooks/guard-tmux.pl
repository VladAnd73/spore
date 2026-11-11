#!/usr/bin/env perl
# PreToolUse hook: keep agents off the operator's real tmux server for
# throwaway work. A bare `tmux kill-server` takes down every project
# session. A worker's scratch `tmux new-session` outlives the worker,
# and a name ending in /coordinator is taken for a real coordinator by
# spore-attach. Both must name a private socket with -L or -S.
#
# The command is split into shell words, so tmux only counts in command
# position: `echo "tmux kill-server"` passes, while `(tmux kill-server)`,
# `timeout 5 tmux kill-server` and `bash -c 'tmux kill-server'` do not.
# A heredoc body is read as commands, which errs on the side of denying.
use strict;
use warnings;
use JSON::PP;

my $raw = do { local $/; <STDIN> };
my $payload = eval { decode_json($raw) };
exit 0 unless ref($payload) eq 'HASH';
exit 0 unless ($payload->{tool_name} // '') eq 'Bash';
my $cmd = $payload->{tool_input}{command} // '';
my $worker = ($ENV{SPORE_TASK_SLUG} // '') ne '';

# Words that may precede the real command, and which of their flags
# take a separate argument. timeout also takes a duration.
my %wrapper = (
    sudo    => [qw(-u -g -C -h -p -r -t -U -D -R)],
    doas    => [qw(-u -C)],
    env     => [qw(-u -C -S)],
    exec    => [qw(-a)],
    command => [],
    nohup   => [],
    setsid  => [],
    stdbuf  => [qw(-i -o -e)],
    time    => [],
    nice    => [qw(-n)],
    timeout => [qw(-s -k)],
    xargs   => [qw(-I -n -P -L -s -d -a -E)],
    watch   => [qw(-n -d)],
);
my %keyword = map { $_ => 1 } qw(if then else elif do while until fi done ! { } time);

# Returns the matching `)` offset for a `$(` body starting at $i.
sub balanced {
    my ($s, $i) = @_;
    my $depth = 1;
    my $start = $i;
    while ($i < length $s) {
        my $c = substr($s, $i, 1);
        if ($c eq '\\') { $i += 2; next }
        if ($c eq "'") {
            my $j = index($s, "'", $i + 1);
            $i = $j < 0 ? length $s : $j + 1;
            next;
        }
        $depth++ if $c eq '(';
        $depth-- if $c eq ')';
        return (substr($s, $start, $i - $start), $i + 1) if $depth == 0;
        $i++;
    }
    return (substr($s, $start), length $s);
}

# Splits $s into simple commands (array refs of words) and the bodies
# of command substitutions, which run wherever they appear.
sub parse {
    my ($s) = @_;
    my (@cmds, @subs, @words, $word);
    my $n = length $s;
    my $i = 0;
    my $end_word = sub { push @words, $word if defined $word; undef $word };
    my $end_cmd = sub { $end_word->(); push @cmds, [@words] if @words; @words = () };
    while ($i < $n) {
        my $c = substr($s, $i, 1);
        if ($c eq '\\') {
            $word .= substr($s, $i + 1, 1) unless substr($s, $i + 1, 1) eq "\n";
            $i += 2;
        } elsif ($c eq "'") {
            my $j = index($s, "'", $i + 1);
            $j = $n if $j < 0;
            $word = ($word // '') . substr($s, $i + 1, $j - $i - 1);
            $i = $j + 1;
        } elsif ($c eq '"') {
            $word //= '';
            $i++;
            while ($i < $n && substr($s, $i, 1) ne '"') {
                my $d = substr($s, $i, 1);
                if ($d eq '\\') {
                    $word .= substr($s, $i + 1, 1);
                    $i += 2;
                } elsif (substr($s, $i, 2) eq '$(') {
                    my ($body, $next) = balanced($s, $i + 2);
                    push @subs, $body;
                    $word .= "\$($body)";
                    $i = $next;
                } elsif ($d eq '`') {
                    my $j = index($s, '`', $i + 1);
                    $j = $n if $j < 0;
                    push @subs, substr($s, $i + 1, $j - $i - 1);
                    $i = $j + 1;
                } else {
                    $word .= $d;
                    $i++;
                }
            }
            $i++;
        } elsif (substr($s, $i, 2) eq '$(') {
            my ($body, $next) = balanced($s, $i + 2);
            push @subs, $body;
            $word .= "\$($body)";
            $i = $next;
        } elsif ($c eq '`') {
            my $j = index($s, '`', $i + 1);
            $j = $n if $j < 0;
            push @subs, substr($s, $i + 1, $j - $i - 1);
            $i = $j + 1;
        } elsif ($c eq '#' && !defined $word) {
            my $j = index($s, "\n", $i);
            $i = $j < 0 ? $n : $j;
        } elsif ($c eq "\n" || $c eq ';' || $c eq '|' || $c eq '(' || $c eq ')') {
            $end_cmd->();
            $i++;
        } elsif ($c eq '&') {
            if (defined $word && $word =~ /[<>]$/ or substr($s, $i + 1, 1) eq '>') {
                $word .= $c;
            } else {
                $end_cmd->();
            }
            $i++;
        } elsif ($c =~ /\s/) {
            $end_word->();
            $i++;
        } else {
            $word .= $c;
            $i++;
        }
    }
    $end_cmd->();
    return (\@cmds, \@subs);
}

sub base { my ($w) = @_; $w =~ s{.*/}{}; return $w }

# Returns 'kill-server', 'new-session' or undef for a tmux argv.
sub tmux_verb {
    my @args = @_;
    while (@args) {
        my $a = shift @args;
        if ($a eq '--') {
            $a = shift @args;
            return undef unless defined $a;
        } elsif ($a =~ /^-(.+)/) {
            my $letters = $1;
            for my $k (0 .. length($letters) - 1) {
                my $l = substr($letters, $k, 1);
                next unless $l =~ /[cfLST]/;
                return undef if $l eq 'L' || $l eq 'S';
                shift @args if $k == length($letters) - 1;
                last;
            }
            next;
        }
        return 'kill-server' if length($a) >= 8 && index('kill-server', $a) == 0;
        return 'new-session' if $a eq 'new' || (length($a) >= 5 && index('new-session', $a) == 0);
        return undef;
    }
    return undef;
}

sub verb_of_words {
    my ($depth, @w) = @_;
    while (@w) {
        if ($keyword{$w[0]} || $w[0] =~ /^\w+=/) { shift @w; next }
        if ($w[0] =~ /^\d*[<>]/) {
            my $r = shift @w;
            shift @w if $r =~ /^\d*[<>]+&?$/;
            next;
        }
        my $b = base($w[0]);
        last unless exists $wrapper{$b};
        shift @w;
        my %takes = map { $_ => 1 } @{ $wrapper{$b} };
        while (@w && ($w[0] =~ /^-/ || ($b eq 'env' && $w[0] =~ /^\w+=/))) {
            my $f = shift @w;
            shift @w if $takes{$f};
        }
        shift @w if $b eq 'timeout';
    }
    return undef unless @w;
    my $b = base(shift @w);
    return tmux_verb(@w) if $b eq 'tmux';
    if ($b =~ /^(?:ba|da|k|z)?sh$/) {
        while (@w && $w[0] =~ /^[-+]/) {
            my $f = shift @w;
            return verb_of($depth + 1, $w[0] // '') if $f =~ /^-\w*c/;
        }
        return undef;
    }
    return verb_of($depth + 1, join(' ', @w)) if $b eq 'eval';
    return undef;
}

sub verb_of {
    my ($depth, $s) = @_;
    return undef if $depth > 4;
    my ($cmds, $subs) = parse($s);
    for my $sub (@$subs) {
        my $v = verb_of($depth + 1, $sub);
        return $v if $v && ($v eq 'kill-server' || $worker);
    }
    for my $c (@$cmds) {
        my $v = verb_of_words($depth, @$c);
        return $v if $v && ($v eq 'kill-server' || $worker);
    }
    return undef;
}

my $verb = verb_of(0, $cmd);
exit 0 unless defined $verb;
my $why = $verb eq 'kill-server'
    ? "a bare `tmux kill-server` kills the operator's real server and every project session on it"
    : "a worker's `tmux new-session` on the real server outlives the task and can be mistaken for a project session";

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
