#!/bin/sh
# list-todos: print every TODO line below the folder named
grep -rn TODO "${1:-.}"
