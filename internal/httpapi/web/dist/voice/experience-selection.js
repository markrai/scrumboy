import { getLocale } from '../i18n/index.js';
import { LOCAL_TEXT_GENERATION_CAPABILITY, } from '../platform/local-text-generation.js';
import { getAppRuntime } from '../platform/runtime.js';
import { SPEECH_INPUT_CAPABILITY, } from '../platform/speech-input.js';
function throwIfAborted(signal) {
    if (signal?.aborted)
        throw new DOMException('VoiceFlow selection cancelled.', 'AbortError');
}
export async function selectVoiceFlowExperience(options = {}) {
    throwIfAborted(options.signal);
    if ((options.locale ?? getLocale()) !== 'en') {
        return { kind: 'legacy-deterministic', reason: 'locale' };
    }
    const runtime = options.runtime ?? getAppRuntime();
    const localTextGeneration = runtime.capability(LOCAL_TEXT_GENERATION_CAPABILITY);
    if (!localTextGeneration)
        return { kind: 'legacy-deterministic', reason: 'ai-absent' };
    const speechInput = runtime.capability(SPEECH_INPUT_CAPABILITY);
    if (!speechInput)
        return { kind: 'legacy-deterministic', reason: 'speech-absent' };
    let aiStatus = null;
    let speechStatus = null;
    try {
        [aiStatus, speechStatus] = await Promise.all([
            localTextGeneration.status({ signal: options.signal }),
            speechInput.status({ signal: options.signal }),
        ]);
    }
    catch {
        throwIfAborted(options.signal);
        return {
            kind: 'enhanced-not-ready',
            localTextGeneration,
            speechInput,
            aiStatus,
            speechStatus,
            reason: 'status-error',
        };
    }
    throwIfAborted(options.signal);
    if (aiStatus.state === 'unsupported') {
        return { kind: 'legacy-deterministic', reason: 'ai-unsupported' };
    }
    if (speechStatus.state === 'unsupported') {
        return { kind: 'legacy-deterministic', reason: 'speech-unsupported' };
    }
    if (aiStatus.state === 'ready' && speechStatus.state === 'ready') {
        return { kind: 'enhanced-agent', localTextGeneration, speechInput };
    }
    return {
        kind: 'enhanced-not-ready',
        localTextGeneration,
        speechInput,
        aiStatus,
        speechStatus,
        reason: aiStatus.state === 'ready' ? 'speech' : 'ai',
    };
}
