package cpictl.standin;

import com.sap.gateway.ip.core.customdev.util.AttachmentWrapper;
import com.sap.gateway.ip.core.customdev.util.Message;
import com.sap.gateway.ip.core.customdev.util.SoapHeader;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import javax.activation.DataHandler;
import org.apache.camel.attachment.Attachment;

/** The version 1 message: what a script gets as {@code message}. */
public class MessageV1 extends MessageCore implements Message {
    private final Map<String, AttachmentWrapper> attachments = new LinkedHashMap<>();

    /**
     * For JavaScript: {@code message.getBody(java.lang.String)} passes Nashorn's StaticClass, which the
     * runtime's Nashorn turns into the class. Groovy and Java pick {@link #getBody(Class)}.
     */
    public Object getBody(Object type) {
        return getBody(jsClass(type));
    }

    public <T> T getHeader(String headerName, Class<T> headerType) {
        return Bodies.convert(headers.get(headerName), headerType);
    }

    /** For JavaScript, like {@link #getBody(Object)}. */
    public Object getHeader(String name, Object type) {
        return getHeader(name, jsClass(type));
    }

    static Class<?> jsClass(Object type) {
        if (type instanceof Class<?> c) {
            return c;
        }
        if (type != null && type.getClass().getName().equals("jdk.dynalink.beans.StaticClass")) {
            try {
                return (Class<?>) type.getClass().getMethod("getRepresentedClass").invoke(type);
            } catch (ReflectiveOperationException e) {
                throw new IllegalStateException(e);
            }
        }
        throw new IllegalArgumentException("not a class: " + type);
    }

    @Override
    public Map<String, DataHandler> getAttachments() {
        Map<String, DataHandler> out = new LinkedHashMap<>();
        attachments.forEach((k, v) -> out.put(k, v.getDataHandler()));
        return out;
    }

    @Override
    public void setAttachments(Map<String, DataHandler> map) {
        attachments.clear();
        if (map != null) {
            map.forEach((k, v) -> attachments.put(k, new AttachmentWrapper(v)));
        }
    }

    @Override
    public long getAttachmentsSize() {
        long n = 0;
        for (AttachmentWrapper a : attachments.values()) {
            n += size(a.getDataHandler());
        }
        return n;
    }

    static long size(DataHandler dh) {
        return Bodies.attachmentBytes(dh).length;
    }

    @Override
    public void addAttachmentHeader(String headerName, String headerValue, AttachmentWrapper attachment) {
        attachment.addHeader(headerName, headerValue);
    }

    @Override
    public void setAttachmentHeader(String headerName, String headerValue, AttachmentWrapper attachment) {
        attachment.setHeader(headerName, headerValue);
    }

    @Override
    public String getAttachmentHeader(String headerName, AttachmentWrapper attachment) {
        return attachment.getHeader(headerName);
    }

    @Override
    public void removeAttachmentHeader(String headerName, AttachmentWrapper attachment) {
        attachment.removeHeader(headerName);
    }

    @Override
    public Map<String, AttachmentWrapper> getAttachmentWrapperObjects() {
        return attachments;
    }

    @Override
    public void setAttachmentWrapperObjects(Map<String, AttachmentWrapper> attachmentObjects) {
        attachments.clear();
        if (attachmentObjects != null) {
            attachments.putAll(attachmentObjects);
        }
    }

    @Override
    public void addAttachmentObject(String id, AttachmentWrapper content) {
        attachments.put(id, content);
    }

    @Override
    @Deprecated
    public void addAttachmentHeader(String headerName, String headerValue, Attachment attachment) {
        attachment.addHeader(headerName, headerValue);
    }

    @Override
    @Deprecated
    public void setAttachmentHeader(String headerName, String headerValue, Attachment attachment) {
        attachment.setHeader(headerName, headerValue);
    }

    @Override
    @Deprecated
    public String getAttachmentHeader(String headerName, Attachment attachment) {
        return attachment.getHeader(headerName);
    }

    @Override
    @Deprecated
    public void removeAttachmentHeader(String headerName, Attachment attachment) {
        attachment.removeHeader(headerName);
    }

    @Override
    @Deprecated
    public Map<String, Attachment> getAttachmentObjects() {
        return new LinkedHashMap<>(attachments);
    }

    @Override
    @Deprecated
    public void setAttachmentObjects(Map<String, Attachment> attachmentObjects) {
        attachments.clear();
        if (attachmentObjects != null) {
            attachmentObjects.forEach((id, a) -> addAttachmentObject(id, a));
        }
    }

    @Override
    @Deprecated
    public void addAttachmentObject(String id, Attachment content) {
        if (content instanceof AttachmentWrapper w) {
            attachments.put(id, w);
            return;
        }
        AttachmentWrapper w = new AttachmentWrapper(content.getDataHandler());
        for (String name : content.getHeaderNames()) {
            for (String v : content.getHeaderAsList(name)) {
                w.addHeader(name, v);
            }
        }
        attachments.put(id, w);
    }

    @Override
    public List<SoapHeader> getSoapHeaders() {
        return new ArrayList<>(soapHeaders);
    }

    @Override
    public void setSoapHeaders(List<SoapHeader> headers) {
        soapHeaders.clear();
        if (headers != null) {
            soapHeaders.addAll(headers);
        }
    }
}
