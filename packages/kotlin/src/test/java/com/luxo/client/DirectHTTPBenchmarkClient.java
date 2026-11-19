package com.luxo.client;

import okhttp3.OkHttpClient;

/** Keeps the synchronous benchmark's newCall override when redirects are disabled. */
abstract class DirectHTTPBenchmarkClient extends OkHttpClient {
    DirectHTTPBenchmarkClient() {
        super(new OkHttpClient.Builder().followRedirects(false).followSslRedirects(false));
    }
}
