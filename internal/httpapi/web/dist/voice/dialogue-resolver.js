import { normalizeLookup } from './normalize.js';
function namedMatches(wantedText, values) {
    const wanted = normalizeLookup(wantedText);
    if (!wanted)
        return [];
    const exact = values
        .map((value, index) => ({ value: normalizeLookup(value), index }))
        .filter(({ value }) => value === wanted)
        .map(({ index }) => index);
    if (exact.length > 0)
        return exact;
    return values
        .map((value, index) => ({ value: normalizeLookup(value), index }))
        .filter(({ value }) => {
        const components = value.split(' ').filter(Boolean);
        return value.startsWith(`${wanted} `)
            || components.includes(wanted)
            || (wanted.length >= 2 && components.some((component) => component.startsWith(wanted)));
    })
        .map(({ index }) => index);
}
function selectedResult(pending, index) {
    const choice = pending.choices[index];
    if (!choice)
        return Object.freeze({ kind: 'no-match' });
    const selected = choice.kind === 'todo'
        ? {
            todo: {
                selectedLocalId: choice.reference.localId,
                allowedLocalIds: pending.choices
                    .filter((candidate) => candidate.kind === 'todo')
                    .map((candidate) => candidate.reference.localId),
            },
        }
        : choice.kind === 'member'
            ? {
                member: {
                    selectedUserId: choice.userId,
                    allowedUserIds: pending.choices
                        .filter((candidate) => candidate.kind === 'member')
                        .map((candidate) => candidate.userId),
                },
            }
            : {
                tag: {
                    selectedName: choice.name,
                    allowedNames: pending.choices
                        .filter((candidate) => candidate.kind === 'tag')
                        .map((candidate) => candidate.name),
                },
            };
    return Object.freeze({
        kind: 'selected',
        index,
        selection: Object.freeze({ ...pending.selection, ...selected }),
    });
}
function finishMatches(pending, matches) {
    if (matches.length === 0)
        return Object.freeze({ kind: 'no-match' });
    if (matches.length > 1) {
        return Object.freeze({ kind: 'ambiguous', indexes: Object.freeze([...matches]) });
    }
    return selectedResult(pending, matches[0]);
}
export function resolvePendingClarificationSelector(pending, selector) {
    if (selector.kind === 'ordinal') {
        return selectedResult(pending, selector.index - 1);
    }
    const first = pending.choices[0];
    if (!first)
        return Object.freeze({ kind: 'no-match' });
    if (first.kind === 'todo') {
        const choices = pending.choices.filter((choice) => choice.kind === 'todo');
        if (selector.kind === 'local-id') {
            return finishMatches(pending, choices
                .map((choice, index) => ({ choice, index }))
                .filter(({ choice }) => choice.reference.localId === selector.localId)
                .map(({ index }) => index));
        }
        if (selector.kind === 'lane') {
            const wanted = normalizeLookup(selector.text);
            return finishMatches(pending, choices
                .map((choice, index) => ({ choice, index }))
                .filter(({ choice }) => [choice.laneName, choice.laneKey]
                .some((value) => normalizeLookup(value) === wanted))
                .map(({ index }) => index));
        }
        return Object.freeze({ kind: 'no-match' });
    }
    if (first.kind === 'member') {
        if (selector.kind !== 'text' && selector.kind !== 'email') {
            return Object.freeze({ kind: 'no-match' });
        }
        const members = pending.choices.filter((choice) => choice.kind === 'member');
        const values = members.map((choice) => selector.kind === 'email' ? choice.email : choice.name);
        return finishMatches(pending, namedMatches(selector.text, values));
    }
    if (selector.kind !== 'text')
        return Object.freeze({ kind: 'no-match' });
    const tags = pending.choices.filter((choice) => choice.kind === 'tag');
    return finishMatches(pending, namedMatches(selector.text, tags.map((choice) => choice.name)));
}
export function pendingForTodoCorrection(choices, selection) {
    return Object.freeze({
        kind: 'clarification',
        intent: Object.freeze({ kind: 'open-todo', target: Object.freeze({ kind: 'current' }) }),
        choices: Object.freeze([...choices]),
        selection,
    });
}
export function resolveRetainedTodoSelector(choices, selection, selector) {
    return resolvePendingClarificationSelector(pendingForTodoCorrection(choices, selection), selector);
}
