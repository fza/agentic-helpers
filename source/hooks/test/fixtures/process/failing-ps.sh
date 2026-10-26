#!/bin/sh
# Stands in for `ps` on a PATH: any read of the process table through it
# fails, so a reader that still spawns `ps` reads nothing.
exit 1
