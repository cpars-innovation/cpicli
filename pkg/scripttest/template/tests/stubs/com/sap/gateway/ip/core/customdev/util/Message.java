package com.sap.gateway.ip.core.customdev.util;

import java.util.List;
import java.util.Map;
import javax.activation.DataHandler;

/**
 * Stand-in of SAP's script API message (version 1 scripts), written from SAP's published API
 * documentation. The implementation is {@code cpictl.standin.MessageV1}.
 */
public interface Message {
    <T> T getBody(Class<T> type);

    Object getBody();

    void setBody(Object exchangeBody);

    Map<String, DataHandler> getAttachments();

    void setAttachments(Map<String, DataHandler> attachments);

    Map<String, Object> getHeaders();

    <T> T getHeader(String headerName, Class<T> headerType);

    void setHeaders(Map<String, Object> exchangeHeaders);

    void setHeader(String name, Object value);

    Map<String, Object> getProperties();

    void setProperties(Map<String, Object> exchangeProperties);

    void setProperty(String name, Object value);

    Object getProperty(String name);

    long getBodySize();

    long getAttachmentsSize();

    void addAttachmentHeader(String headerName, String headerValue, AttachmentWrapper attachment);

    void setAttachmentHeader(String headerName, String headerValue, AttachmentWrapper attachment);

    String getAttachmentHeader(String headerName, AttachmentWrapper attachment);

    void removeAttachmentHeader(String headerName, AttachmentWrapper attachment);

    Map<String, AttachmentWrapper> getAttachmentWrapperObjects();

    void setAttachmentWrapperObjects(Map<String, AttachmentWrapper> attachmentObjects);

    void addAttachmentObject(String id, AttachmentWrapper content);

    @Deprecated
    void addAttachmentHeader(String headerName, String headerValue, org.apache.camel.attachment.Attachment attachment);

    @Deprecated
    void setAttachmentHeader(String headerName, String headerValue, org.apache.camel.attachment.Attachment attachment);

    @Deprecated
    String getAttachmentHeader(String headerName, org.apache.camel.attachment.Attachment attachment);

    @Deprecated
    void removeAttachmentHeader(String headerName, org.apache.camel.attachment.Attachment attachment);

    @Deprecated
    Map<String, org.apache.camel.attachment.Attachment> getAttachmentObjects();

    @Deprecated
    void setAttachmentObjects(Map<String, org.apache.camel.attachment.Attachment> attachmentObjects);

    @Deprecated
    void addAttachmentObject(String id, org.apache.camel.attachment.Attachment content);

    List<SoapHeader> getSoapHeaders();

    void setSoapHeaders(List<SoapHeader> headers);

    void clearSoapHeaders();
}
