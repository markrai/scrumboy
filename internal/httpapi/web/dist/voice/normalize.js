const SMALL = {
    zero: 0,
    oh: 0,
    one: 1,
    two: 2,
    three: 3,
    four: 4,
    five: 5,
    six: 6,
    seven: 7,
    eight: 8,
    nine: 9,
    ten: 10,
    eleven: 11,
    twelve: 12,
    thirteen: 13,
    fourteen: 14,
    fifteen: 15,
    sixteen: 16,
    seventeen: 17,
    eighteen: 18,
    nineteen: 19,
};
const TENS = {
    twenty: 20,
    thirty: 30,
    forty: 40,
    fourty: 40,
    fifty: 50,
    sixty: 60,
    seventy: 70,
    eighty: 80,
    ninety: 90,
};
function normalizeNumberPhrase(input) {
    const tokens = normalizePhrase(input)
        .split(" ")
        .filter(Boolean);
    if (tokens[0] === "#" || tokens[0]?.startsWith("#")) {
        tokens[0] = tokens[0].replace(/^#/, "");
    }
    if (tokens[0] === "number" || tokens[0] === "id") {
        tokens.shift();
    }
    if (tokens[0] === "#" || tokens[0]?.startsWith("#")) {
        tokens[0] = tokens[0].replace(/^#/, "");
    }
    return tokens.filter(Boolean);
}
export function normalizePhrase(input) {
    return String(input ?? "")
        .toLowerCase()
        .replace(/[\u2018\u2019]/g, "'")
        .replace(/[\u201c\u201d]/g, '"')
        .replace(/[_-]+/g, " ")
        .replace(/[.,!?;:()[\]{}]+/g, " ")
        .replace(/\s+/g, " ")
        .trim();
}
export function normalizeLookup(input) {
    return normalizePhrase(input)
        .replace(/['"]/g, "")
        .replace(/\s+/g, " ")
        .trim();
}
export function stripWrappingQuotes(input) {
    const trimmed = input.trim();
    if (trimmed.length >= 2) {
        const first = trimmed[0];
        const last = trimmed[trimmed.length - 1];
        if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
            return trimmed.slice(1, -1).trim();
        }
    }
    return trimmed;
}
export function containsProjectScopeOverride(input) {
    const normalized = normalizePhrase(input);
    return /\b(in|on|for|under|inside)\s+(the\s+)?project\b/.test(normalized)
        || /\bproject\s+(named|called|slug|is)\b/.test(normalized)
        || /\bswitch\s+(to\s+)?project\b/.test(normalized);
}
export function parseSpokenNumber(input) {
    const trimmed = String(input ?? "").trim();
    const digits = trimmed.match(/^#?\s*(\d+)$/);
    if (digits) {
        const value = Number(digits[1]);
        return Number.isSafeInteger(value) && value > 0 ? { value, ambiguous: false } : null;
    }
    const tokens = normalizeNumberPhrase(trimmed);
    if (tokens.length === 0)
        return null;
    if (tokens.length === 1 && /^\d+$/.test(tokens[0])) {
        const value = Number(tokens[0]);
        return Number.isSafeInteger(value) && value > 0 ? { value, ambiguous: false } : null;
    }
    if (tokens.every((token) => Object.prototype.hasOwnProperty.call(SMALL, token) && SMALL[token] >= 0 && SMALL[token] <= 9)) {
        if (tokens.length === 1) {
            const value = SMALL[tokens[0]];
            return value > 0 ? { value, ambiguous: false } : null;
        }
        const value = Number(tokens.map((token) => String(SMALL[token])).join(""));
        return Number.isSafeInteger(value) && value > 0 ? { value, ambiguous: true } : null;
    }
    let total = 0;
    let current = 0;
    let sawNumber = false;
    for (const token of tokens) {
        if (token === "and")
            continue;
        if (Object.prototype.hasOwnProperty.call(SMALL, token)) {
            current += SMALL[token];
            sawNumber = true;
            continue;
        }
        if (Object.prototype.hasOwnProperty.call(TENS, token)) {
            current += TENS[token];
            sawNumber = true;
            continue;
        }
        if (token === "hundred") {
            if (current === 0)
                return null;
            current *= 100;
            sawNumber = true;
            continue;
        }
        if (token === "thousand") {
            if (current === 0)
                return null;
            total += current * 1000;
            current = 0;
            sawNumber = true;
            continue;
        }
        return null;
    }
    const value = total + current;
    if (!sawNumber || !Number.isSafeInteger(value) || value <= 0)
        return null;
    return { value, ambiguous: false };
}
