// A request with no deadline: the I/O half of HISS-02 is not decided for TypeScript.
export async function status(url: string): Promise<number> {
  const response = await fetch(url, { method: "HEAD" });
  return response.status;
}
