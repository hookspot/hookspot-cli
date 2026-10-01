#!/usr/bin/env fish
# Builds an unpublished binary for a non-prod server; build-dev.fish and
# build-stage.fish wrap it. The build runs in Docker against the checkout, so
# it lands in tmp/dist and --out copies it from there.

function usage
    gum style --border rounded --padding "0 1" --border-foreground 212 -- \
        "Usage: scripts/build-$argv[1].fish [--os OS[/ARCH]] [--out PATH]" \
        "" \
        "--os   darwin, linux or windows; ARCH is amd64 or arm64 (default: this machine's)" \
        "       Linux binaries are static, so they run on Alpine too." \
        "--out  directory (e.g. ~/Downloads/) or file to copy the binary to"
end

cd (status dirname)/..
set -l prefix $argv[1]
set -l server_url $argv[2]

argparse h/help 'os=' 'out=' -- $argv[3..]
or begin
    usage $prefix
    exit 1
end
if set -q _flag_help
    usage $prefix
    exit 0
end

set -l host_arch (uname -m | string replace x86_64 amd64)
set -l goos darwin
set -l goarch $host_arch
set -l built tmp/dist/hookspot_$prefix

if set -q _flag_os
    set -l target (string split / -- $_flag_os)
    set goos $target[1]
    set -q target[2]; and set goarch $target[2]
    if not contains -- $goos darwin linux windows; or not contains -- $goarch amd64 arm64
        gum style --foreground 196 "Unsupported target $goos/$goarch"
        usage $prefix
        exit 1
    end
    test $goos = windows; and set built $built.exe
end

gum style --foreground 212 "Building hookspot_$prefix for $goos/$goarch"
make local-build CONFIG_PREFIX=$prefix SERVER_URL=$server_url LOCAL_GOOS=$goos LOCAL_GOARCH=$goarch LOCAL_OUTPUT=$built
or exit 1

set -l result $built
if set -q _flag_out
    set result $_flag_out
    if string match -q -- '*/' $_flag_out; or test -d $_flag_out
        mkdir -p $_flag_out
        set result (string trim -r -c / -- $_flag_out)/(path basename $built)
    end
    cp $built $result
    or exit 1
end

gum style --foreground 212 "Built $result"
