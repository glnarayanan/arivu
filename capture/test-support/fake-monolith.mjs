#!/usr/bin/env node

if (process.argv.includes('--version')) {
  process.stdout.write('monolith 2.10.1\n');
  process.exit(0);
}

process.stdin.pipe(process.stdout);
