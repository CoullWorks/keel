#!/bin/sh
# keel pre_build hook — fires ONCE, before the recipe loop begins.
#
# Wired from example-addon.yaml with `script: hooks/pre_build.sh`. keel runs it as
# `sh hooks/pre_build.sh` in the project directory. Use pre_build for a one-time
# precondition check or setup that must happen before any recipe installs.
#
# Pack hooks are UNTRUSTED: keel shows the command and asks before running it.
echo "example-pack pre_build: nothing installed yet, this runs first (in $(pwd))"
