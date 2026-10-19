#!/bin/sh
# A child in a process group of its own, out of reach of a group kill, holding
# the output pipes.
set -m
sleep 60 &
echo $! > "$1"
wait
