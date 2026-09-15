// The credential is read from the environment; nothing secret is in the file.
export function token(): string | undefined {
  return process.env.SERVICE_API_TOKEN;
}
