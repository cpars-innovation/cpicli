package com.sap.it.script.v2.api.exceptions;

import java.util.Map;

/** Stand-in: an HTTP call answered with an error status. */
public class HttpOperationFailedException extends ScenarioExecutionException {
    private static final long serialVersionUID = 1L;

    private final String url;
    private final int statusCode;
    private final String statusText;
    private final String location;
    private final transient Map<String, String> responseHeaders;
    private final String responseBody;

    public HttpOperationFailedException(String url, int statusCode, String statusText, String location,
                                        Map<String, String> responseHeaders, String responseBody) {
        super("HTTP operation failed invoking " + printableUri(url) + " with statusCode: " + statusCode
                + (location != null ? ", redirectLocation: " + location : ""));
        this.url = url;
        this.statusCode = statusCode;
        this.statusText = statusText;
        this.location = location;
        this.responseHeaders = responseHeaders;
        this.responseBody = responseBody;
    }

    public String getUrl() {
        return url;
    }

    public boolean isRedirectError() {
        return statusCode >= 300 && statusCode < 400;
    }

    public boolean hasRedirectLocation() {
        return location != null;
    }

    public String getRedirectLocation() {
        return location;
    }

    public int getStatusCode() {
        return statusCode;
    }

    public String getStatusText() {
        return statusText;
    }

    public Map<String, String> getResponseHeaders() {
        return responseHeaders;
    }

    public String getResponseBody() {
        return responseBody;
    }

    /** The URI without user info (credentials never appear in messages). */
    public static String printableUri(String uri) {
        if (uri == null) {
            return null;
        }
        return uri.replaceAll("(?<=://)[^/@]*@", "");
    }
}
