import { describe, expect, it } from "vitest";
import {
  buildTodoCreatePayload,
  buildTodoPatchPayload,
  shouldSubmitSprintAssignment,
} from "./todo-submit.js";

describe("todo submit payload helpers", () => {
  it("preserves raw markdown in create payloads", () => {
    const body = "# Heading\n\n- item\n\n`code`";
    const payload = buildTodoCreatePayload({
      title: "Plain title",
      body,
      tags: ["Bug"],
      columnKey: "backlog",
      estimationRaw: "5",
      sprintEnabled: true,
      sprintId: 12,
      assigneeEnabled: true,
      assigneeUserId: 44,
	  priorityKey: null,
    });

    expect(payload).toEqual({
      title: "Plain title",
      body,
      tags: ["Bug"],
      columnKey: "backlog",
      estimationPoints: 5,
      sprintId: 12,
      assigneeUserId: 44,
      priorityKey: null,
    });
  });

  it("preserves raw markdown in patch payloads without converting to html", () => {
    const body = "## Notes\n\n**still markdown**";
    const payload = buildTodoPatchPayload({
      title: "**plain title**",
      body,
      tags: [],
      estimationRaw: "invalid",
      assigneeEnabled: true,
      assigneeUserId: null,
	  priorityKey: null,
      sprintEnabled: false,
      sprintId: 8,
    });

    expect(payload).toEqual({
      title: "**plain title**",
      body,
      tags: [],
      assigneeUserId: null,
      priorityKey: null,
    });
    expect(JSON.stringify(payload)).not.toContain("<h2>");
    expect(JSON.stringify(payload)).not.toContain("<strong>");
  });

  it("includes and normalizes priority selection", () => {
    expect(buildTodoCreatePayload({
      title: "Title", body: "", tags: [], columnKey: "backlog", priorityKey: "urgent",
    }).priorityKey).toBe("urgent");
    expect(buildTodoPatchPayload({
      title: "Title", body: "", tags: [], priorityKey: "",
    }).priorityKey).toBeNull();
  });

  it("submits sprint assignment only when enabled controls change the value", () => {
    expect(shouldSubmitSprintAssignment(false, 8, null)).toBe(false);
    expect(shouldSubmitSprintAssignment(true, null, null)).toBe(false);
    expect(shouldSubmitSprintAssignment(true, 8, 8)).toBe(false);
    expect(shouldSubmitSprintAssignment(true, 8, null)).toBe(true);
    expect(shouldSubmitSprintAssignment(true, null, 8)).toBe(true);
    expect(shouldSubmitSprintAssignment(true, 8, 9)).toBe(true);
  });
});
