package com.markrai.scrumboy.transport;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertThrows;
import static org.junit.Assert.assertTrue;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import org.junit.Test;

public class NativeResourceStoreTest {
    @Test
    public void createsCommitsAndReleasesOpaqueResourcesIdempotently() throws Exception {
        File cache = Files.createTempDirectory("scrumboy-resource-test").toFile();
        NativeResourceStore store = new NativeResourceStore(cache);
        NativeResourceStore.PendingResource pending = store.create();
        byte[] content = new byte[] { 1, 2, 3, 4 };
        Files.write(pending.partial().toPath(), content);

        File completed = store.commit(pending);
        assertTrue(completed.exists());
        assertArrayEquals(content, Files.readAllBytes(completed.toPath()));
        store.release(pending.handle());
        store.release(pending.handle());
        assertFalse(completed.exists());
    }

    @Test
    public void cancellationRemovesPartialAndCompletedFiles() throws Exception {
        File cache = Files.createTempDirectory("scrumboy-resource-cancel").toFile();
        NativeResourceStore store = new NativeResourceStore(cache);
        NativeResourceStore.PendingResource pending = store.create();
        Files.write(pending.partial().toPath(), "partial".getBytes(StandardCharsets.UTF_8));

        store.cancel(pending);
        assertFalse(pending.partial().exists());
        assertFalse(pending.completed().exists());
    }

    @Test
    public void sessionResetInvalidatesAnAcquisitionThatTriesToCommitLate() throws Exception {
        File cache = Files.createTempDirectory("scrumboy-resource-reset").toFile();
        NativeResourceStore store = new NativeResourceStore(cache);
        NativeResourceStore.PendingResource pending = store.create();
        Files.write(pending.partial().toPath(), new byte[] { 9, 8, 7 });

        store.releaseAll();

        assertThrows(IOException.class, () -> store.commit(pending));
        assertFalse(pending.partial().exists());
        assertFalse(pending.completed().exists());
    }

    @Test
    public void rejectsPathsOutsideDedicatedCacheDirectory() throws Exception {
        File cache = Files.createTempDirectory("scrumboy-resource-containment").toFile();
        NativeResourceStore store = new NativeResourceStore(cache);
        assertThrows(IOException.class, () -> store.contained(new File(store.directory(), "../escape.resource")));
    }

    @Test
    public void startupDeletesStaleOwnedFiles() throws Exception {
        File cache = Files.createTempDirectory("scrumboy-resource-stale").toFile();
        File directory = new File(cache, NativeResourceStore.DIRECTORY_NAME);
        assertTrue(directory.mkdirs());
        File stale = new File(directory, "stale.resource");
        Files.write(stale.toPath(), "stale".getBytes(StandardCharsets.UTF_8));

        new NativeResourceStore(cache);
        assertFalse(stale.exists());
    }
}
