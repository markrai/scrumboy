export type SpeechResult = {
  alternatives: string[];
};

type SpeechRecognitionConstructor = new () => SpeechRecognitionLike;

type SpeechRecognitionLike = EventTarget & {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  maxAlternatives: number;
  start(): void;
  stop(): void;
  abort(): void;
  onresult: ((event: SpeechRecognitionEventLike) => void) | null;
  onerror: ((event: SpeechRecognitionErrorEventLike) => void) | null;
  onend: (() => void) | null;
};

type SpeechRecognitionEventLike = {
  results: ArrayLike<ArrayLike<{ transcript: string }>>;
};

type SpeechRecognitionErrorEventLike = {
  error?: string;
  message?: string;
};

declare global {
  interface Window {
    SpeechRecognition?: SpeechRecognitionConstructor;
    webkitSpeechRecognition?: SpeechRecognitionConstructor;
  }
}

function getSpeechRecognitionCtor(): SpeechRecognitionConstructor | null {
  return window.SpeechRecognition ?? window.webkitSpeechRecognition ?? null;
}

export function isSpeechRecognitionSupported(): boolean {
  return !!getSpeechRecognitionCtor();
}

export function startOneShotRecognition(options: {
  signal?: AbortSignal;
  timeoutMs?: number;
  lang?: string;
} = {}): Promise<SpeechResult> {
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
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

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

    const settleResolve = (result: SpeechResult) => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(result);
    };

    const settleReject = (err: Error) => {
      if (settled) return;
      settled = true;
      cleanup();
      try {
        recognition.abort();
      } catch {
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
      const alternatives: string[] = [];
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
      } catch {
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
    } catch (err: any) {
      settleReject(new Error(err?.message || "Speech recognition could not start."));
    }
  });
}
