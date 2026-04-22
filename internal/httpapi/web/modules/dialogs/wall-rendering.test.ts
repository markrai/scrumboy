// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import {
  beginEdgePreview,
  buildNoteElement,
  clampDim,
  ensureEdgeOverlay,
  enterEditMode,
  exitEditMode,
  isEditing,
  MIN_NOTE_WIDTH,
  MAX_NOTE_WIDTH,
  noteStyleAttr,
  renderEdges,
  renderEmptyWallHtml,
  sanitizeNoteColor,
  updateEdgesForNote,
  type WallNote,
} from './wall-rendering.js';

function note(overrides: Partial<WallNote> = {}): WallNote {
  return {
    id: 'n1',
    x: 10,
    y: 20,
    width: 200,
    height: 160,
    color: '#FFFFFF',
    text: 'hello',
    version: 3,
    ...overrides,
  };
}

describe('wall-rendering helpers', () => {
  it('clamps dimensions inside the plan-defined bounds', () => {
    expect(clampDim(10, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH)).toBe(MIN_NOTE_WIDTH);
    expect(clampDim(9999, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH)).toBe(MAX_NOTE_WIDTH);
    expect(clampDim(250, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH)).toBe(250);
    expect(clampDim(Number.NaN, MIN_NOTE_WIDTH, MAX_NOTE_WIDTH)).toBe(MIN_NOTE_WIDTH);
  });

  it('falls back to the default color when the value is not a hex triplet', () => {
    expect(sanitizeNoteColor('#abc')).toMatch(/^#([0-9a-f]{6})$/i);
    expect(sanitizeNoteColor('not-a-color')).toBe('#ffd966');
    expect(sanitizeNoteColor(null)).toBe('#ffd966');
    expect(sanitizeNoteColor('')).toBe('#ffd966');
  });

  it('builds a CSS style attribute from note geometry', () => {
    const style = noteStyleAttr(note({ color: '#ffd966' }));
    expect(style).toContain('left:10px');
    expect(style).toContain('top:20px');
    expect(style).toContain('width:200px');
    expect(style).toContain('height:160px');
    expect(style).toContain('background:#ffd966');
  });

  it('renders a note in display mode by default (no always-on textarea)', () => {
    const el = buildNoteElement(note({ text: 'hello\nworld' }), true);
    expect(el.classList.contains('wall-note')).toBe(true);
    expect(el.dataset.noteId).toBe('n1');
    expect(el.dataset.version).toBe('3');
    // Display mode: textContent preserves newlines; no textarea present yet.
    const display = el.querySelector<HTMLElement>('.wall-note__display');
    expect(display).not.toBeNull();
    expect(display!.textContent).toBe('hello\nworld');
    expect(el.querySelector('textarea')).toBeNull();
    expect(el.querySelector('.wall-note__delete')).toBeNull();
    // Editable notes still show the resize handle per user decision.
    expect(el.querySelector('.wall-note__resize-handle')).not.toBeNull();
  });

  it('renders a read-only note without the resize handle', () => {
    const el = buildNoteElement(note(), false);
    expect(el.querySelector('.wall-note__display')).not.toBeNull();
    expect(el.querySelector('textarea')).toBeNull();
    expect(el.querySelector('.wall-note__resize-handle')).toBeNull();
  });

  it('stamps data-color-index from the palette', () => {
    const white = buildNoteElement(note({ color: '#FFFFFF' }), true);
    expect(white.dataset.colorIndex).toBe('0');
    const orange = buildNoteElement(note({ color: '#FF7F00' }), true);
    expect(orange.dataset.colorIndex).toBe('2');
    const unknown = buildNoteElement(note({ color: '#123456' }), true);
    expect(unknown.dataset.colorIndex).toBe('0');
  });

  it('swaps display div for textarea on enterEditMode and restores on exit', () => {
    const el = buildNoteElement(note({ text: 'original' }), true);
    expect(isEditing(el)).toBe(false);
    const ta = enterEditMode(el, 'original');
    expect(ta).not.toBeNull();
    expect(ta.tagName).toBe('TEXTAREA');
    expect(ta.value).toBe('original');
    expect(el.querySelector('.wall-note__display')).toBeNull();
    expect(isEditing(el)).toBe(true);
    // Second call returns the same textarea, doesn't duplicate.
    const ta2 = enterEditMode(el, 'different');
    expect(ta2).toBe(ta);
    // exitEditMode restores a display div with the supplied text.
    exitEditMode(el, 'committed');
    expect(el.querySelector('textarea')).toBeNull();
    expect(el.querySelector('.wall-note__display')?.textContent).toBe('committed');
    expect(isEditing(el)).toBe(false);
  });

  it('renders a helpful empty state per role', () => {
    expect(renderEmptyWallHtml(true)).toContain('Right-click');
    expect(renderEmptyWallHtml(true)).toContain('Shift');
    expect(renderEmptyWallHtml(false)).toContain('Waiting for a contributor');
  });
});

describe('wall edge overlay', () => {
  function mountSurfaceWithNotes(): { surface: HTMLElement; a: HTMLElement; b: HTMLElement } {
    const surface = document.createElement('div');
    Object.defineProperty(surface, 'offsetWidth', { configurable: true, value: 1000 });
    Object.defineProperty(surface, 'offsetHeight', { configurable: true, value: 800 });
    surface.style.position = 'relative';
    document.body.appendChild(surface);
    const a = buildNoteElement(
      { id: 'na', x: 100, y: 100, width: 200, height: 100, color: '#FFFFFF', text: '', version: 1 },
      true,
    );
    const b = buildNoteElement(
      { id: 'nb', x: 400, y: 200, width: 200, height: 100, color: '#FFFFFF', text: '', version: 1 },
      true,
    );
    surface.append(a, b);
    // happy-dom does not compute layout; emulate offsetLeft/Top/Width/Height
    // so getNoteCenterFromElement and noteCenter() can resolve coordinates.
    function stubBox(el: HTMLElement, l: number, t: number, w: number, h: number) {
      Object.defineProperty(el, 'offsetLeft', { configurable: true, value: l });
      Object.defineProperty(el, 'offsetTop', { configurable: true, value: t });
      Object.defineProperty(el, 'offsetWidth', { configurable: true, value: w });
      Object.defineProperty(el, 'offsetHeight', { configurable: true, value: h });
    }
    stubBox(a, 100, 100, 200, 100);
    stubBox(b, 400, 200, 200, 100);
    return { surface, a, b };
  }

  it('ensureEdgeOverlay creates a single SVG that ignores pointer events', () => {
    const surface = document.createElement('div');
    document.body.appendChild(surface);
    const svg = ensureEdgeOverlay(surface);
    expect(svg.tagName.toLowerCase()).toBe('svg');
    expect(svg.style.pointerEvents).toBe('none');
    // Idempotent: second call returns the same node, doesn't append a second.
    const svg2 = ensureEdgeOverlay(surface);
    expect(svg2).toBe(svg);
    expect(surface.querySelectorAll('svg').length).toBe(1);
  });

  it('renderEdges draws hit + visible lines between note centers', () => {
    const { surface } = mountSurfaceWithNotes();
    renderEdges(surface, [{ id: 'e1', from: 'na', to: 'nb' }]);
    const groups = surface.querySelectorAll('.wall-edge-group');
    expect(groups.length).toBe(1);
    const g = groups[0] as SVGGElement;
    expect(g.dataset.edgeId).toBe('e1');
    const hit = g.querySelector('.wall-edge-hit') as SVGLineElement;
    const line = g.querySelector('.wall-edge-line') as SVGLineElement;
    expect(hit).not.toBeNull();
    expect(line).not.toBeNull();
    // Centers: A=(200,150), B=(500,250). Both lines must agree.
    expect(hit.getAttribute('x1')).toBe('200');
    expect(hit.getAttribute('y1')).toBe('150');
    expect(line.getAttribute('x2')).toBe('500');
    expect(line.getAttribute('y2')).toBe('250');
    // Hit line must be the only one that opts into pointer events so the
    // overlay never blocks note clicks.
    expect(hit.getAttribute('pointer-events')).toBe('stroke');
  });

  it('skips edges with missing endpoints (e.g. note removed locally)', () => {
    const { surface } = mountSurfaceWithNotes();
    renderEdges(surface, [
      { id: 'good', from: 'na', to: 'nb' },
      { id: 'orphan', from: 'na', to: 'gone' },
      { id: 'self', from: 'na', to: 'na' },
    ]);
    expect(surface.querySelectorAll('.wall-edge-group').length).toBe(1);
  });

  it('updateEdgesForNote moves only the lines that touch the given note', () => {
    const { surface } = mountSurfaceWithNotes();
    renderEdges(surface, [{ id: 'e1', from: 'na', to: 'nb' }]);
    updateEdgesForNote(surface, 'na', 999, 999);
    const line = surface.querySelector('.wall-edge-line') as SVGLineElement;
    expect(line.getAttribute('x1')).toBe('999');
    expect(line.getAttribute('y1')).toBe('999');
    // The opposite endpoint must be untouched.
    expect(line.getAttribute('x2')).toBe('500');
    expect(line.getAttribute('y2')).toBe('250');
  });

  it('beginEdgePreview adds and removes a dashed preview line', () => {
    const surface = document.createElement('div');
    document.body.appendChild(surface);
    const ctrl = beginEdgePreview(surface, { cx: 10, cy: 20 });
    expect(surface.querySelector('.wall-edge-preview')).not.toBeNull();
    ctrl.update(50, 60);
    const ln = surface.querySelector('.wall-edge-preview') as SVGLineElement;
    expect(ln.getAttribute('x2')).toBe('50');
    expect(ln.getAttribute('y2')).toBe('60');
    ctrl.end();
    expect(surface.querySelector('.wall-edge-preview')).toBeNull();
  });
});
