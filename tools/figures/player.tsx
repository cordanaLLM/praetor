// The interactive player: React plus the vendored interfig Flow component. The loader imports this
// chunk only when a figure nears the viewport, so pages without figures never download React.
import { createRoot } from 'react-dom/client';
import { Flow } from './third_party/interfig/upstream/src/index.tsx';
import type { FlowProps } from './third_party/interfig/upstream/src/model.ts';
import { enhanceTabs } from './keyboard.ts';

export type MountOptions = {
  /** Off under prefers-reduced-motion: the player then shows each step's last beat and no packets. */
  autoplay: boolean;
  /** Names the scenario tab list for assistive technology. */
  label: string;
};

/** Renders `props` into `host` and returns the function that unmounts it again. */
export function mount(host: HTMLElement, props: FlowProps, options: MountOptions): () => void {
  const root = createRoot(host);
  root.render(<Flow {...props} autoplay={options.autoplay} />);
  const release = enhanceTabs(host, options.label);
  return () => {
    release();
    root.unmount();
  };
}
