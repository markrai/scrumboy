package com.markrai.scrumboy.transport;

import java.io.File;
import java.io.IOException;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

final class NativeResourceStore {
    static final String DIRECTORY_NAME = "scrumboy-server-resources";

    static final class PendingResource {
        private final String handle;
        private final File partial;
        private final File completed;
        private final long generation;

        PendingResource(String handle, File partial, File completed, long generation) {
            this.handle = handle;
            this.partial = partial;
            this.completed = completed;
            this.generation = generation;
        }

        String handle() { return handle; }
        File partial() { return partial; }
        File completed() { return completed; }
    }

    private final File directory;
    private final ConcurrentHashMap<String, File> resources = new ConcurrentHashMap<>();
    private final AtomicLong generation = new AtomicLong();

    NativeResourceStore(File cacheDirectory) throws IOException {
        directory = new File(cacheDirectory, DIRECTORY_NAME).getCanonicalFile();
        if (!directory.exists() && !directory.mkdirs()) {
            throw new IOException("Could not create resource cache");
        }
        cleanupStaleFiles();
    }

    PendingResource create() throws IOException {
        String handle = UUID.randomUUID().toString();
        File partial = contained(new File(directory, handle + ".partial"));
        File completed = contained(new File(directory, handle + ".resource"));
        return new PendingResource(handle, partial, completed, generation.get());
    }

    synchronized File commit(PendingResource pending) throws IOException {
        if (pending.generation != generation.get()) {
            cancel(pending);
            throw new IOException("Resource acquisition was invalidated");
        }
        if (!pending.partial().renameTo(pending.completed())) {
            throw new IOException("Could not finalize resource");
        }
        resources.put(pending.handle(), pending.completed());
        return pending.completed();
    }

    void cancel(PendingResource pending) {
        delete(pending.partial());
        delete(pending.completed());
        resources.remove(pending.handle());
    }

    synchronized void release(String handle) {
        if (handle == null) return;
        File resource = resources.remove(handle);
        if (resource != null) delete(resource);
    }

    synchronized void releaseAll() {
        generation.incrementAndGet();
        for (String handle : resources.keySet()) release(handle);
    }

    File directory() {
        return directory;
    }

    private void cleanupStaleFiles() {
        File[] children = directory.listFiles();
        if (children == null) return;
        for (File child : children) {
            try {
                delete(contained(child));
            } catch (IOException ignored) {
                // Ignore a child that fails containment validation.
            }
        }
    }

    File contained(File file) throws IOException {
        File canonical = file.getCanonicalFile();
        String prefix = directory.getPath() + File.separator;
        if (!canonical.getPath().startsWith(prefix)) throw new IOException("Resource path escaped cache directory");
        return canonical;
    }

    private static void delete(File file) {
        if (file.exists()) file.delete();
    }
}
