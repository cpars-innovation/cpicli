package com.sap.gateway.ip.core.customdev.util;

import java.util.HashMap;
import java.util.Map;

public class MessageLog {
    private final Map<String, String> customHeaders = new HashMap<>();
    private final Map<String, String> stringProperties = new HashMap<>();
    private final Map<String, String> attachments = new HashMap<>();

    public void addCustomHeaderProperty(String name, String value) { customHeaders.put(name, value); }
    public void setStringProperty(String name, String value) { stringProperties.put(name, value); }
    public void addAttachmentAsString(String name, String text, String type) { attachments.put(name, text); }
    public Map<String, String> getCustomHeaders() { return customHeaders; }
    public Map<String, String> getStringProperties() { return stringProperties; }
    public Map<String, String> getAttachments() { return attachments; }
}
