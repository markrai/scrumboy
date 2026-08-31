package com.markrai.scrumboy;

import android.os.Bundle;
import com.getcapacitor.BridgeActivity;
import com.markrai.scrumboy.localai.ScrumboyLocalTextGenerationPlugin;
import com.markrai.scrumboy.speech.ScrumboySpeechInputPlugin;
import com.markrai.scrumboy.transport.ScrumboyTransportPlugin;

public class MainActivity extends BridgeActivity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        registerPlugin(ScrumboyLocalTextGenerationPlugin.class);
        registerPlugin(ScrumboySpeechInputPlugin.class);
        registerPlugin(ScrumboyTransportPlugin.class);
        super.onCreate(savedInstanceState);
    }
}
