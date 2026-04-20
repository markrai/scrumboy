function getSpeechRecognitionCtor() {
    return window.SpeechRecognition ?? window.webkitSpeechRecognition ?? null;
}
export function isSpeechRecognitionSupported() {
    return !!getSpeechRecognitionCtor();
}
export function startOneShotRecognition(options = {}) {
    return new Promise((resolve, reject) => {
        const Recognition = getSpeechRecognitionCtor();
        if (!Recognition) {
            reject(new Error("Speech recognition is not available in this browser."));
            return;
        }
        const recognition = new Recognition();
        const timeoutMs = options.timeoutMs ?? 12000;
        let settled = false;
        let sawResult = false;
        let timeoutId = null;
        const cleanup = () => {
            if (timeoutId) {
                clearTimeout(timeoutId);
                timeoutId = null;
            }
            recognition.onresult = null;
            recognition.onerror = null;
            recognition.onend = null;
            options.signal?.removeEventListener("abort", onAbort);
        };
        const settleResolve = (result) => {
            if (settled)
                return;
            settled = true;
            cleanup();
            resolve(result);
        };
        const settleReject = (err) => {
            if (settled)
                return;
            settled = true;
            cleanup();
            try {
                recognition.abort();
            }
            catch {
                /* ignore */
            }
            reject(err);
        };
        const onAbort = () => {
            settleReject(new Error("Listening canceled."));
        };
        recognition.continuous = false;
        recognition.interimResults = false;
        recognition.lang = options.lang || navigator.language || "en-US";
        recognition.maxAlternatives = 3;
        recognition.onresult = (event) => {
            sawResult = true;
            const alternatives = [];
            const result = event.results?.[0];
            if (result) {
                for (let i = 0; i < result.length; i += 1) {
                    const transcript = result[i]?.transcript?.trim();
                    if (transcript && !alternatives.includes(transcript)) {
                        alternatives.push(transcript);
                    }
                }
            }
            if (alternatives.length === 0) {
                settleReject(new Error("No speech was recognized."));
                return;
            }
            settleResolve({ alternatives });
            try {
                recognition.stop();
            }
            catch {
                /* ignore */
            }
        };
        recognition.onerror = (event) => {
            const message = event.message || event.error || "Speech recognition failed.";
            settleReject(new Error(message));
        };
        recognition.onend = () => {
            if (!settled && !sawResult) {
                settleReject(new Error("No speech was recognized."));
            }
        };
        if (options.signal?.aborted) {
            onAbort();
            return;
        }
        options.signal?.addEventListener("abort", onAbort, { once: true });
        timeoutId = setTimeout(() => {
            settleReject(new Error("Listening timed out."));
        }, timeoutMs);
        try {
            recognition.start();
        }
        catch (err) {
            settleReject(new Error(err?.message || "Speech recognition could not start."));
        }
    });
}
