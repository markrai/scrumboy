declare global {
  type MarkdownItRendererRule = (
    tokens: any[],
    idx: number,
    options: any,
    env: any,
    self: {
      renderToken(tokens: any[], idx: number, options: any): string;
    },
  ) => string;

  type MarkdownItInstance = {
    render(markdown: string): string;
    parse(markdown: string, env: Record<string, unknown>): any[];
    renderer: {
      rules: Record<string, MarkdownItRendererRule | undefined>;
    };
  };

  type MermaidSecurityLevel = "strict" | "loose" | "antiscript" | "sandbox";

  type MermaidConfig = {
    startOnLoad?: boolean;
    securityLevel?: MermaidSecurityLevel;
    maxTextSize?: number;
    maxEdges?: number;
    suppressErrorRendering?: boolean;
    theme?: string;
    themeVariables?: Record<string, string | boolean>;
  };

  type MermaidRunOptions = {
    nodes?: ArrayLike<HTMLElement>;
    querySelector?: string;
    suppressErrors?: boolean;
    postRenderCallback?: (id: string) => unknown;
  };

  type MermaidInstance = {
    initialize(config: MermaidConfig): void;
    run(options?: MermaidRunOptions): Promise<void>;
  };

  type MarkdownItFactory = (
    presetName?: string,
    options?: {
      html?: boolean;
      breaks?: boolean;
      linkify?: boolean;
    },
  ) => MarkdownItInstance;

  type DOMPurifyLike = {
    sanitize(dirty: string, config?: Record<string, unknown>): string;
  };

  interface Window {
    markdownit?: MarkdownItFactory;
    DOMPurify?: DOMPurifyLike;
    mermaid?: MermaidInstance;
  }
}

export {};
