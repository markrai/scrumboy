/**
 * The Capacitor bridge buffers whole request and response bodies as base64 inside the WebView,
 * so native clients refuse backup transfers above this size (and never move .sbz archives).
 */
export const NATIVE_BACKUP_MAX_BYTES = 8 * 1024 * 1024;
