// The spec format for praetor's documentation figures (docs/figures/<slug>.ts).
//
// It narrows interfig's own types (third_party/interfig/upstream/src/model.ts) to strings: the
// React player, the exported SVG and the derived text description must show the same content,
// and the SVG exporter can only draw text. build.mjs enforces the same rules at runtime, so a
// spec that bypasses the type checker still fails the build.
import type { FigTone } from '../../third_party/interfig/upstream/src/model.ts';

/** A box: `store` draws a database cylinder for data at rest, `decision` a diamond. */
export type PraetorNode = {
  id: string;
  label: string;
  sub?: string;
  shape?: 'box' | 'decision' | 'store';
  lines?: number;
  width?: number;
};

/** A group of boxes and groups. Only a group with a `label` gets a frame. */
export type PraetorGroup = {
  id?: string;
  label?: string;
  direction?: 'row' | 'column';
  gap?: number;
  align?: 'start' | 'center' | 'end';
  children: (PraetorNode | PraetorGroup)[];
};

/** `from` and `to` name a box or a group id; the default edge id is `from->to`. */
export type PraetorEdge = {
  id?: string;
  from: string;
  to: string;
  label?: string;
  around?: 'above' | 'below';
  quiet?: boolean;
};

/** One packet on one edge; `data` rides along as a small card. */
export type PraetorHop = string | { edge: string; back?: boolean; data?: string };

/** One row of a content card. */
export type PraetorRow = {
  tag?: string;
  tone?: FigTone;
  text: string;
  meta?: string;
  mark?: string;
  mono?: boolean;
};

/** One beat of a step: packets cross `edges`, `say` narrates, `show` fills content cards. */
export type PraetorBeat = {
  edges?: PraetorHop | PraetorHop[];
  say?: string;
  show?: Record<string, PraetorRow[] | string>;
  light?: string[];
  ms?: number;
};

/** One scenario tab: its `flow` beats play in order. */
export type PraetorStep = {
  label: string;
  caption?: string;
  flow: (PraetorHop | PraetorHop[] | PraetorBeat)[];
  nodes?: string[];
};

/** What the player and the SVG exporter draw. */
export type PraetorProps = {
  layout: PraetorGroup;
  edges: PraetorEdge[];
  steps?: PraetorStep[];
  speed?: number;
};

/**
 * One documentation figure.
 *
 * - `alt` is one sentence of at most 125 characters.
 * - `evidence` lists `path:Symbol` anchors the figure was drawn from; `docs_diagrams.py sources`
 *   fails when a path is missing or the symbol no longer occurs in it.
 * - `describe` adds lines to the derived text description.
 */
export type PraetorFigure = {
  title: string;
  alt: string;
  evidence: string[];
  describe?: string[];
  props: PraetorProps;
};
