// The I/O half of HISS-02: a fetch with no AbortSignal deadline is not decided.
export async function load(url) {
  const response = await fetch(url);
  return response.json();
}
