#!/bin/sh
# Stands in for `ps -o lstart=`: prints one start time, translated as `ps`
# translates it, so a caller that forgets LC_ALL=C reads German text.
if [ "$LC_ALL" = "C" ]; then
	echo "Sat Oct 24 14:35:50 2026"
else
	echo "Sa. 24 Okt. 14:35:50 2026"
fi
