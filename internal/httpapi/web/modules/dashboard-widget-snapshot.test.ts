// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import {
  DASHBOARD_WIDGET_MAX_ITEMS,
  mapDashboardWidgetSnapshot,
} from './dashboard-widget-snapshot.js';
import type { DashboardSummary, DashboardTodo } from './types.js';

function summary(overrides: Partial<DashboardSummary> = {}): DashboardSummary {
  return {
    assignedCount: 2,
    totalAssignedStoryPoints: 5,
    wipCount: 1,
    projects: [{
      projectId: 10,
      projectName: 'Alpha',
      projectSlug: 'alpha',
      activeSprint: { id: 3, name: 'Sprint 1', startAt: 1, endAt: 2 },
      sprintSections: [{ id: 3, name: 'Sprint 1', state: 'ACTIVE' }],
    }],
    ...overrides,
  };
}

function todo(overrides: Partial<DashboardTodo> = {}): DashboardTodo {
  return {
    id: 100,
    localId: 12,
    title: 'Fix login',
    projectId: 10,
    projectName: 'Alpha',
    projectSlug: 'alpha',
    projectDominantColor: '#888888',
    status: 'doing',
    statusName: 'In progress',
    statusColor: '#60a5fa',
    updatedAt: '2026-01-01T00:00:00.000Z',
    sprintId: 3,
    estimationPoints: 2,
    ...overrides,
  };
}

describe('mapDashboardWidgetSnapshot', () => {
  it('maps assigned and WIP counts with sanitized item fields', () => {
    const payload = mapDashboardWidgetSnapshot({
      userId: 7,
      fetchedAtMs: 1_700_000_000_000,
      summary: summary(),
      todos: [todo()],
    });
    expect(payload).toEqual({
      userId: 7,
      fetchedAtMs: 1_700_000_000_000,
      assignedCount: 2,
      wipCount: 1,
      items: [{
        localId: 12,
        title: 'Fix login',
        projectName: 'Alpha',
        projectSlug: 'alpha',
        statusName: 'In progress',
        statusColor: '#60a5fa',
        estimationPoints: 2,
        sprintName: 'Sprint 1',
      }],
    });
  });

  it('omits notes, email, assignee, priority, and project images', () => {
    const payload = mapDashboardWidgetSnapshot({
      userId: 1,
      fetchedAtMs: 10,
      summary: summary(),
      todos: [todo({
        projectImage: 'https://example/secret.png',
        priorityKey: 'high',
        title: 'Visible',
      } as DashboardTodo)],
    });
    expect(payload?.items[0]).not.toHaveProperty('notes');
    expect(payload?.items[0]).not.toHaveProperty('body');
    expect(payload?.items[0]).not.toHaveProperty('email');
    expect(payload?.items[0]).not.toHaveProperty('assignee');
    expect(payload?.items[0]).not.toHaveProperty('priorityKey');
    expect(payload?.items[0]).not.toHaveProperty('projectImage');
  });

  it('maps empty todos and caps persisted items', () => {
    expect(mapDashboardWidgetSnapshot({
      userId: 1,
      fetchedAtMs: 10,
      summary: summary({ assignedCount: 0, wipCount: 0 }),
      todos: [],
    })?.items).toEqual([]);

    const todos = Array.from({ length: 8 }, (_, index) => todo({ localId: index + 1, title: `T${index + 1}` }));
    const payload = mapDashboardWidgetSnapshot({
      userId: 1,
      fetchedAtMs: 10,
      summary: summary({ assignedCount: 8, wipCount: 8 }),
      todos,
    });
    expect(payload?.items).toHaveLength(DASHBOARD_WIDGET_MAX_ITEMS);
  });

  it('drops unsafe status colors', () => {
    const payload = mapDashboardWidgetSnapshot({
      userId: 1,
      fetchedAtMs: 10,
      summary: summary(),
      todos: [todo({ statusColor: 'red' })],
    });
    expect(payload?.items[0].statusColor).toBeUndefined();
  });

  it('returns null for invalid user or timestamp', () => {
    expect(mapDashboardWidgetSnapshot({
      userId: 0,
      fetchedAtMs: 10,
      summary: summary(),
      todos: [],
    })).toBeNull();
  });
});
