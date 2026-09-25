import { AgentProtocolError } from './agent-protocol.js';
let nextTask = 0;
/** Handles never alias across tasks, even when a new task starts in the same session. */
export class VoiceAgentResourceHandles {
    constructor() {
        this.prefix = ++nextTask;
        this.values = new Map();
    }
    issue(resource) {
        const same = JSON.stringify(resource);
        for (const [handle, value] of this.values)
            if (JSON.stringify(value) === same)
                return handle;
        if (this.values.size >= 40)
            throw new AgentProtocolError('Resource limit');
        const handle = `${resource.kind}_${this.prefix}_${this.values.size + 1}`;
        this.values.set(handle, Object.freeze({ ...resource }));
        return handle;
    }
    get(handle, kind) {
        const value = this.values.get(handle);
        if (!value || value.kind !== kind)
            throw new AgentProtocolError('Unknown resource handle');
        return value;
    }
    clear() { this.values.clear(); }
}
