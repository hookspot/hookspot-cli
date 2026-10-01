#!/usr/bin/env fish
cd (status dirname)/..
make local-build CONFIG_PREFIX=dev SERVER_URL=https://hookspot.localhost:4443
and gum style --foreground 212 "Built tmp/dist/hookspot_dev"
