package com.markrai.scrumboy.transport;

final class TransportException extends Exception {
    private final String code;

    TransportException(String code, String message) {
        super(message);
        this.code = code;
    }

    TransportException(String code, String message, Throwable cause) {
        super(message, cause);
        this.code = code;
    }

    String code() {
        return code;
    }
}
