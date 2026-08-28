#!/bin/sh
# keel post_create hook — fires once, after the FRAMEWORK skeleton exists.
#
# WIRING: a hook script is not run on its own. A recipe references it from its
# `hooks:` block with `script: hooks/post_create.sh`; keel then runs it as
# `sh hooks/post_create.sh` in the project directory. See example-service.yaml.
#
# post_create fires ONLY for a framework recipe. This pack ships no framework, so
# this script is here to document the shape for a pack that DOES.
#
# Pack hooks are UNTRUSTED: on the first build that uses them keel prints the exact
# command and asks before running it. keel is offline-by-design.
#
# PASSING VALUES: keel renders the recipe's own {{tokens}} (e.g. {{project}},
# {{env}}, {{db.name}}) into the recipe's run:/script: strings BEFORE running them.
# A script that needs a value should take it as an argument the recipe passes,
# e.g.  script: hooks/post_create.sh   with the recipe using run: to pass args —
# the script's working directory is the project root either way.
echo "example-pack post_create: framework skeleton is ready (running in $(pwd))"
