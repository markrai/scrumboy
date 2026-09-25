package com.markrai.scrumboy.transport;

import java.util.List;
import okhttp3.MediaType;
import okhttp3.MultipartBody;
import okhttp3.RequestBody;

final class NativeRequestBodies {
    static final class Field {
        private final String name;
        private final String value;
        private final String filename;
        private final String contentType;
        private final byte[] bytes;

        private Field(String name, String value, String filename, String contentType, byte[] bytes) {
            this.name = name;
            this.value = value;
            this.filename = filename;
            this.contentType = contentType;
            this.bytes = bytes;
        }

        static Field text(String name, String value) {
            return new Field(name, value, null, null, null);
        }

        static Field file(String name, String filename, String contentType, byte[] bytes) {
            return new Field(name, null, filename, contentType, bytes);
        }
    }

    private NativeRequestBodies() {}

    static RequestBody text(String data, String contentType) {
        MediaType mediaType = contentType == null ? null : MediaType.parse(contentType);
        return RequestBody.create(data, mediaType);
    }

    static RequestBody multipart(List<Field> fields) {
        MultipartBody.Builder multipart = new MultipartBody.Builder().setType(MultipartBody.FORM);
        for (Field field : fields) {
            if (field.bytes == null) {
                multipart.addFormDataPart(field.name, field.value);
            } else {
                MediaType mediaType = MediaType.parse(field.contentType);
                multipart.addFormDataPart(field.name, field.filename, RequestBody.create(field.bytes, mediaType));
            }
        }
        return multipart.build();
    }
}
