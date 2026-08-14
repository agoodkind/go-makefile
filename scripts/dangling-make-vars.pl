#!/usr/bin/env perl
# Report every make variable the pipeline uses that nothing in the pipeline
# defines.
#
# A rename that moves a definition without moving its uses leaves the uses
# expanding to nothing, which make reports as a recipe running an empty
# command rather than as an error. Checking that a name is still mentioned
# somewhere does not catch it, because the stale uses are themselves
# mentions. This checks definitions against uses instead.
#
# Usage: dangling-make-vars.pl <make file> ...
# Exits non-zero when a used name has no definition.

use strict;
use warnings;

my (%defined, %used, %first_use);

for my $path (@ARGV) {
    open my $handle, '<', $path or die "open $path: $!";
    my $line_number = 0;
    while (my $line = <$handle>) {
        $line_number++;

        # Every assignment operator make accepts, so a name defined with any of
        # them is not reported as dangling. != runs a shell command, and ::=
        # and :::= are the POSIX simple forms.
        if ($line =~ /^\s*(?:export\s+)?([A-Za-z_][\w-]*)\s*(?::::=|::=|:=|\?=|\+=|!=|=[^=])/) {
            $defined{$1} = 1;
        }
        if ($line =~ /^\s*export\s+([A-Za-z_][\w-]*)\s*$/) { $defined{$1} = 1 }
        if ($line =~ /^\s*define\s+([A-Za-z_][\w-]*)/)     { $defined{$1} = 1 }

        next if $line =~ /^\s*#/;

        while ($line =~ /\$\(([A-Za-z_][\w-]*)\)/g) {
            $used{$1} = 1;
            $first_use{$1} ||= "$path:$line_number";
        }
        while ($line =~ /\$\(call\s+([A-Za-z_][\w-]*)\s*,/g) {
            $used{$1} = 1;
            $first_use{$1} ||= "$path:$line_number";
        }
    }
    close $handle;
}

# Names make itself provides, names a consumer supplies, names that arrive
# from the environment, and the single-letter $(foreach) loop variables.
my %supplied_elsewhere = map { $_ => 1 } qw(
    CURDIR MAKE MAKEFILE_LIST MAKEFLAGS MFLAGS MAKELEVEL SHELL
    HOME PWD PATH USER TMPDIR GOPATH GOBIN
    BINARY CMD VPKG GKLOG_VPKG LIBRARY XDG_BIN_HOME CGO_ENABLED GOFLAGS
    GOOS GOARCH CERT_ID LAUNCHD_LABEL SYSTEMD_UNIT RELEASE_STAGE
    GO_MK_CC GO_MK_CXX GO_MK_SKIP_FETCH _GO_MK_PROVISIONED
    uname d m s
);

my $dangling = 0;
for my $name (sort keys %used) {
    next if $defined{$name} || $supplied_elsewhere{$name};
    printf "dangling: %s is used at %s but never defined\n", $name, $first_use{$name};
    $dangling++;
}

if ($dangling) {
    printf "%d dangling make variable(s)\n", $dangling;
    exit 1;
}
printf "make variables ok (%d used, %d defined)\n",
    scalar(keys %used), scalar(keys %defined);
