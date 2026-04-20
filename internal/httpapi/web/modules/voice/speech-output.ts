export function prepareTextForSpeechSynthesis(text: string): string {
  return String(text ?? "").replace(/^(Create|Open|Delete|Move|Assign) todo\b/i, "$1 to do");
}

export function speak(text: string, options: { signal?: AbortSignal } = {}): Promise<void> {
  return new Promise((resolve) => {
    const synth = window.speechSynthesis;
    if (!synth || typeof SpeechSynthesisUtterance === "undefined") {
      resolve();
      return;
    }

    let settled = false;
    const utterance = new SpeechSynthesisUtterance(prepareTextForSpeechSynthesis(text));
    const cleanup = () => {
      utterance.onend = null;
      utterance.onerror = null;
      options.signal?.removeEventListener("abort", onAbort);
    };
    const finish = () => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve();
    };
    const onAbort = () => {
      try {
        synth.cancel();
      } catch {
      }
      finish();
    };

    utterance.onend = finish;
    utterance.onerror = finish;
    if (options.signal?.aborted) {
      onAbort();
      return;
    }
    options.signal?.addEventListener("abort", onAbort, { once: true });
    try {
      synth.cancel();
      synth.speak(utterance);
    } catch {
      finish();
    }
  });
}
