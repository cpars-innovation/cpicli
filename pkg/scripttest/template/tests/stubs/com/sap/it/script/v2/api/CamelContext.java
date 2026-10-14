package com.sap.it.script.v2.api;

/** Stand-in: the context of the integration flow. */
public interface CamelContext {
    String getName();

    long getUptimeMillis();

    String getVersion();

    Language resolveLanguage(String language);
}
