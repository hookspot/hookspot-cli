#!/usr/bin/env fish
cd (status dirname)/..
make local-build CONFIG_PREFIX=stage SERVER_URL=https://app.stage-m0rk7a.hookspot.dev
and gum style --foreground 212 "Built tmp/dist/hookspot_stage"
