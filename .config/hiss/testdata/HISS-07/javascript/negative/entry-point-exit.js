// The entry point may end the process: module scope and a top-level main.
async function main() {
  const code = await run();
  process.exit(code);
}

main().catch(() => process.exit(2));
