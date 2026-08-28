#!/bin/sh
# keel post_build hook — fires ONCE, after the whole recipe loop finishes.
#
# Wired from example-addon.yaml with `script: hooks/post_build.sh`. keel runs it as
# `sh hooks/post_build.sh` in the finished project directory. Use post_build for a
# final step that needs every recipe already installed: a one-off codegen, a
# summary, a "you're ready" message.
#
# Pack hooks are UNTRUSTED: keel shows the command and asks before running it.
echo "example-pack post_build: every recipe is installed, the build is done (in $(pwd))"
