// HISS-11: a module is imported straight from a CDN at a floating "latest" specifier,
// so no lockfile entry and no integrity hash covers the code that actually executes.
import { render } from "https://cdn.example.com/render@latest/index.js";

export function draw(node: HTMLElement): void {
  render(node);
}
