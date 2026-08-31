import { LocalTextGenerationError, } from '../platform/local-text-generation.js';
import { generateVoiceInterpretationRaw, parseVoiceInterpretationEnvelopeDetails, validateVoiceInterpretationCandidate, } from './local-interpretation.js';
import { INTERPRETATION_LAB_CANDIDATE_PROFILE, INTERPRETATION_LAB_CURRENT_PROFILE, INTERPRETATION_LAB_EXPERIMENTAL_PROFILE, interpretationLabInstructions, } from './interpretation-lab-prompts.js';
import { parseCommand } from './parser.js';
import { resolveCommandDraft } from './resolve.js';
import { isCommandFailure } from './schema.js';
const NOT_RUN = Object.freeze({ state: 'not-run' });
function cancelledDiagnostic(input, profile, rawOutput) {
    return {
        input,
        profile,
        ...(rawOutput === undefined ? {} : { rawOutput }),
        provider: 'cancelled',
        envelope: NOT_RUN,
        canonicalParse: NOT_RUN,
        resolution: NOT_RUN,
        finalClassification: 'cancelled',
    };
}
function stableProviderReason(error) {
    if (error instanceof LocalTextGenerationError)
        return error.code;
    if (error instanceof Error && error.name === 'AbortError')
        return 'cancelled';
    return 'unavailable';
}
function isCancellation(error, signal) {
    return !!signal?.aborted
        || (error instanceof LocalTextGenerationError && error.code === 'cancelled')
        || (error instanceof Error && error.name === 'AbortError');
}
export async function diagnoseVoiceInterpretation(options) {
    const input = String(options.input ?? '').trim();
    const base = { input, profile: options.profile };
    let rawOutput;
    try {
        const generated = await generateVoiceInterpretationRaw({
            transcript: input,
            instructions: interpretationLabInstructions(options.profile),
            capability: options.capability,
            locale: options.locale ?? 'en',
            signal: options.signal,
        });
        rawOutput = generated.rawOutput;
    }
    catch (error) {
        if (isCancellation(error, options.signal))
            return cancelledDiagnostic(input, options.profile);
        const reason = stableProviderReason(error);
        if (error instanceof LocalTextGenerationError && error.code === 'output_rejected') {
            return {
                ...base,
                provider: 'ok',
                envelope: { state: 'rejected', reason: 'generation-output-rejected' },
                canonicalParse: NOT_RUN,
                resolution: NOT_RUN,
                finalClassification: 'output-rejected',
            };
        }
        return {
            ...base,
            provider: 'error',
            envelope: { state: 'rejected', reason: `provider:${reason}` },
            canonicalParse: NOT_RUN,
            resolution: NOT_RUN,
            finalClassification: 'provider-error',
        };
    }
    if (options.signal?.aborted)
        return cancelledDiagnostic(input, options.profile, rawOutput);
    let command;
    let unrepresented;
    try {
        const envelope = parseVoiceInterpretationEnvelopeDetails(rawOutput, input);
        command = envelope.command;
        unrepresented = envelope.unrepresented;
    }
    catch {
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope: { state: 'rejected', reason: 'envelope-or-safety-contract' },
            canonicalParse: NOT_RUN,
            resolution: NOT_RUN,
            finalClassification: 'output-rejected',
        };
    }
    const envelope = command === null || unrepresented.length > 0
        ? { state: 'refused', command, unrepresented }
        : { state: 'candidate', command, unrepresented };
    if (command === null) {
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope,
            canonicalParse: NOT_RUN,
            resolution: NOT_RUN,
            finalClassification: 'model-refused',
        };
    }
    try {
        validateVoiceInterpretationCandidate(input, command);
    }
    catch {
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope: { state: 'rejected', reason: 'candidate-preservation' },
            canonicalParse: NOT_RUN,
            resolution: NOT_RUN,
            finalClassification: 'output-rejected',
        };
    }
    const parsed = parseCommand(command);
    if (isCommandFailure(parsed)) {
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope,
            canonicalParse: { state: 'failure', code: parsed.code, message: parsed.message },
            resolution: NOT_RUN,
            finalClassification: 'canonical-rejected',
        };
    }
    const canonicalParse = { state: 'ok', command };
    if (options.signal?.aborted)
        return cancelledDiagnostic(input, options.profile, rawOutput);
    const context = options.getResolveContext?.() ?? null;
    if (!context) {
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope,
            canonicalParse,
            resolution: NOT_RUN,
            finalClassification: unrepresented.length > 0 ? 'partial' : 'recognized',
        };
    }
    try {
        const resolved = await resolveCommandDraft(parsed.value, {
            projectId: context.projectId,
            projectSlug: context.projectSlug,
            board: context.board,
            members: context.members,
        });
        if (options.signal?.aborted)
            return cancelledDiagnostic(input, options.profile, rawOutput);
        if (isCommandFailure(resolved)) {
            return {
                ...base,
                rawOutput,
                provider: 'ok',
                envelope,
                canonicalParse,
                resolution: { state: 'failure', code: resolved.code, message: resolved.message },
                finalClassification: 'resolution-failed',
            };
        }
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope,
            canonicalParse,
            resolution: { state: 'ok', summary: resolved.value.summary },
            finalClassification: unrepresented.length > 0 ? 'partial' : 'recognized',
        };
    }
    catch {
        return {
            ...base,
            rawOutput,
            provider: 'ok',
            envelope,
            canonicalParse,
            resolution: { state: 'failure', code: 'internal', message: 'Resolution failed.' },
            finalClassification: 'resolution-failed',
        };
    }
}
export async function runInterpretationLabBatch(options) {
    const inputs = options.inputs.map((input) => String(input ?? '').trim()).filter(Boolean);
    const profiles = options.profiles.filter((profile) => (profile === INTERPRETATION_LAB_CURRENT_PROFILE
        || profile === INTERPRETATION_LAB_EXPERIMENTAL_PROFILE
        || profile === INTERPRETATION_LAB_CANDIDATE_PROFILE));
    const total = inputs.length * profiles.length;
    const results = [];
    for (const input of inputs) {
        for (const profile of profiles) {
            if (options.signal?.aborted)
                return results;
            options.onProgress?.({ completed: results.length, total, input, profile });
            const result = await diagnoseVoiceInterpretation({
                input,
                profile,
                capability: options.capability,
                locale: options.locale,
                signal: options.signal,
                getResolveContext: options.getResolveContext,
            });
            results.push(result);
            options.onProgress?.({ completed: results.length, total, input, profile, result });
            if (result.finalClassification === 'cancelled')
                return results;
        }
    }
    return results;
}
