package com.sap.it.script.v2.api;

import com.sap.it.script.v2.api.extensions.ExtensionFunctions;
import java.util.Map;
import javax.activation.DataHandler;

/**
 * Stand-in of the version 2 script API message, written from SAP's published API documentation.
 * The implementation is {@code cpictl.standin.MessageV2}. It has no {@code getHeader(...)}: that is
 * the version 1 API; version 2 scripts read {@code getHeaders().get(name)}.
 */
public interface Message {
    <T> T getBody(Class<T> type);

    Object getBody();

    void setBody(Object exchangeBody);

    Map<String, DataHandler> getAttachments();

    void setAttachments(Map<String, DataHandler> attachments);

    Map<String, Object> getHeaders();

    void setHeaders(Map<String, Object> exchangeHeaders);

    void setHeader(String name, Object value);

    Map<String, Object> getProperties();

    void setProperties(Map<String, Object> exchangeProperties);

    void setProperty(String name, Object value);

    Object getProperty(String name);

    long getBodySize();

    long getAttachmentsSize();

    void addAttachmentHeader(String headerName, String headerValue, Attachment attachment);

    void setAttachmentHeader(String headerName, String headerValue, Attachment attachment);

    String getAttachmentHeader(String headerName, Attachment attachment);

    void removeAttachmentHeader(String headerName, Attachment attachment);

    Map<String, Attachment> getAttachmentObjects();

    void setAttachmentObjects(Map<String, Attachment> attachmentObjects);

    void addAttachmentObject(String id, Attachment content);

    void clearSoapHeaders();

    void clearAttachments();

    void resetBodyStream();

    ExtensionFunctions getExtensionFunctions(Class<? extends ExtensionFunctions> clazz);
}
