// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from 'vitest';
import { resolveDblClickCreateTarget } from './board-dblclick.js';

const FIRST_KEY = 'backlog';

function q(selector: string): Element {
  const el = document.querySelector(selector);
  if (!el) throw new Error(`missing test element: ${selector}`);
  return el;
}

describe('resolveDblClickCreateTarget', () => {
  beforeEach(() => {
    // Mirrors the board view markup: .page > .container > .mobile-board-wrapper > .board > section.col
    document.body.innerHTML = `
      <div class="page">
        <div class="container">
          <div class="filters"><div class="chips" id="tagChips"></div></div>
          <div class="mobile-board-wrapper">
            <div class="mobile-tabs" id="mobileTabs"></div>
            <div class="board">
              <section class="col col--agenda" data-column="agenda">
                <div class="col__head"><span class="col__title">Agenda</span></div>
                <div class="col__list" data-status="agenda"></div>
              </section>
              <section class="col" data-column="backlog">
                <div class="col__head col__head--backlog"><span class="col__title">Not Started</span><span class="col__count">1</span></div>
                <div class="col__list" data-status="backlog">
                  <div class="card" data-todo-id="7" id="todo_7"><span class="card__title">Existing todo</span></div>
                </div>
                <div class="col__load-more" data-load-more="backlog"><button type="button">Load more</button></div>
              </section>
              <section class="col" data-column="inprogress">
                <div class="col__head"><span class="col__title">In Progress</span></div>
                <div class="col__list" data-status="inprogress"></div>
              </section>
            </div>
          </div>
        </div>
      </div>
    `;
  });

  describe('lane targets', () => {
    it('resolves the lane key from its header title', () => {
      expect(resolveDblClickCreateTarget(q('.col__head--backlog .col__title'), FIRST_KEY)).toBe('backlog');
    });

    it('resolves the lane key from empty list space, including in a lane with cards', () => {
      expect(resolveDblClickCreateTarget(q('[data-status="backlog"]'), FIRST_KEY)).toBe('backlog');
    });

    it('resolves the lane key from an empty lane', () => {
      expect(resolveDblClickCreateTarget(q('[data-status="inprogress"]'), FIRST_KEY)).toBe('inprogress');
    });

    it('resolves the lane key from the section element itself', () => {
      expect(resolveDblClickCreateTarget(q('section[data-column="inprogress"]'), FIRST_KEY)).toBe('inprogress');
    });
  });

  describe('background targets resolve to the first workflow column', () => {
    it.each(['.board', '.mobile-board-wrapper', '.container', '.page'])('%s', (selector) => {
      expect(resolveDblClickCreateTarget(q(selector), FIRST_KEY)).toBe(FIRST_KEY);
    });

    it('uses whatever first column key the caller passes (customized workflows)', () => {
      expect(resolveDblClickCreateTarget(q('.board'), 'icebox')).toBe('icebox');
    });
  });

  describe('excluded targets', () => {
    it('ignores cards (single-click edit owns them)', () => {
      expect(resolveDblClickCreateTarget(q('[data-todo-id="7"]'), FIRST_KEY)).toBeNull();
    });

    it('ignores elements inside cards', () => {
      expect(resolveDblClickCreateTarget(q('.card__title'), FIRST_KEY)).toBeNull();
    });

    it('ignores the load-more control and its button', () => {
      expect(resolveDblClickCreateTarget(q('[data-load-more="backlog"]'), FIRST_KEY)).toBeNull();
      expect(resolveDblClickCreateTarget(q('[data-load-more="backlog"] button'), FIRST_KEY)).toBeNull();
    });

    it('ignores the agenda lane (calendar events, not todos)', () => {
      expect(resolveDblClickCreateTarget(q('.col--agenda .col__title'), FIRST_KEY)).toBeNull();
      expect(resolveDblClickCreateTarget(q('[data-status="agenda"]'), FIRST_KEY)).toBeNull();
    });

    it('ignores surfaces that are not board background (filters, mobile tabs)', () => {
      expect(resolveDblClickCreateTarget(q('.filters'), FIRST_KEY)).toBeNull();
      expect(resolveDblClickCreateTarget(q('.mobile-tabs'), FIRST_KEY)).toBeNull();
    });

    it('ignores non-element targets', () => {
      expect(resolveDblClickCreateTarget(null, FIRST_KEY)).toBeNull();
      expect(resolveDblClickCreateTarget(document.createTextNode('x'), FIRST_KEY)).toBeNull();
    });

    it('ignores a column without a data-column key', () => {
      const col = q('section[data-column="inprogress"]');
      col.removeAttribute('data-column');
      expect(resolveDblClickCreateTarget(col, FIRST_KEY)).toBeNull();
    });
  });
});
