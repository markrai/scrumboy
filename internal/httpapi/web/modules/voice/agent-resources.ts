import { AgentProtocolError } from './agent-protocol.js';
export type AgentResource =
  | Readonly<{ kind: 'todo'; localId: number; rowId: number }>
  | Readonly<{ kind: 'member'; userId: number }>
  | Readonly<{ kind: 'tag'; name: string }>
  | Readonly<{ kind: 'lane'; key: string }>;
let nextTask = 0;
/** Handles never alias across tasks, even when a new task starts in the same session. */
export class VoiceAgentResourceHandles {
  private readonly prefix = ++nextTask;
  private readonly values = new Map<string, AgentResource>();
  issue(resource: AgentResource): string {
    const same = JSON.stringify(resource);
    for (const [handle, value] of this.values) if (JSON.stringify(value) === same) return handle;
    if (this.values.size >= 40) throw new AgentProtocolError('Resource limit');
    const handle = `${resource.kind}_${this.prefix}_${this.values.size + 1}`;
    this.values.set(handle, Object.freeze({ ...resource }));
    return handle;
  }
  get<K extends AgentResource['kind']>(handle: string, kind: K): Extract<AgentResource, { kind: K }> {
    const value = this.values.get(handle);
    if (!value || value.kind !== kind) throw new AgentProtocolError('Unknown resource handle');
    return value as Extract<AgentResource, { kind: K }>;
  }
  clear(): void { this.values.clear(); }
}
