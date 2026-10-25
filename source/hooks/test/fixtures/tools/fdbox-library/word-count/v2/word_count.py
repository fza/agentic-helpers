import sys
# skips fenced code blocks
print(len(open(sys.argv[1]).read().split()))
