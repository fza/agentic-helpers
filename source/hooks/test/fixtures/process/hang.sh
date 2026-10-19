#!/bin/sh
# Stands in for `sdd` holding a model call: a child shares the output pipes and
# outlives its parent unless the whole group is killed.
sleep 60 &
echo $! > "$1"
wait
