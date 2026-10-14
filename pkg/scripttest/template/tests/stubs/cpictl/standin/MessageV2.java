package cpictl.standin;

import com.sap.it.script.v2.api.Attachment;
import com.sap.it.script.v2.api.Exchange;
import com.sap.it.script.v2.api.Message;
import com.sap.it.script.v2.api.attachment.AttachmentMap;
import com.sap.it.script.v2.api.extensions.ExchangeExtensionFunctions;
import com.sap.it.script.v2.api.extensions.ExtensionFunctions;
import java.util.LinkedHashMap;
import java.util.Map;
import javax.activation.DataHandler;

/** The version 2 message: what a v2 script gets as {@code message}. */
public final class MessageV2 extends MessageCore implements Message {
    private final Map<String, Attachment> attachments = new LinkedHashMap<>();
    private final ExchangeV2 exchange = new ExchangeV2(this);

    /** The exchange of this message (scripts that take an Exchange get it). */
    public Exchange exchange() {
        return exchange;
    }

    @Override
    public Map<String, DataHandler> getAttachments() {
        return new AttachmentMap(attachments);
    }

    @Override
    public void setAttachments(Map<String, DataHandler> map) {
        attachments.clear();
        if (map != null) {
            new AttachmentMap(attachments).putAll(map);
        }
    }

    @Override
    public long getAttachmentsSize() {
        long n = 0;
        for (Attachment a : attachments.values()) {
            n += MessageV1.size(a.getDataHandler());
        }
        return n;
    }

    @Override
    public void addAttachmentHeader(String headerName, String headerValue, Attachment attachment) {
        attachment.addHeader(headerName, headerValue);
    }

    @Override
    public void setAttachmentHeader(String headerName, String headerValue, Attachment attachment) {
        attachment.setHeader(headerName, headerValue);
    }

    @Override
    public String getAttachmentHeader(String headerName, Attachment attachment) {
        return attachment.getHeader(headerName);
    }

    @Override
    public void removeAttachmentHeader(String headerName, Attachment attachment) {
        attachment.removeHeader(headerName);
    }

    @Override
    public Map<String, Attachment> getAttachmentObjects() {
        return attachments;
    }

    @Override
    public void setAttachmentObjects(Map<String, Attachment> attachmentObjects) {
        attachments.clear();
        if (attachmentObjects != null) {
            attachments.putAll(attachmentObjects);
        }
    }

    @Override
    public void addAttachmentObject(String id, Attachment content) {
        attachments.put(id, content);
    }

    @Override
    public void clearAttachments() {
        attachments.clear();
    }

    @Override
    public void resetBodyStream() {
        if (body instanceof Bodies.CachedStream c) {
            c.reset();
        }
    }

    @Override
    public ExtensionFunctions getExtensionFunctions(Class<? extends ExtensionFunctions> clazz) {
        if (clazz == ExchangeExtensionFunctions.class) {
            long created = CaseContext.current().created;
            return new ExchangeExtensionFunctions() {
                @Override
                public long getCreated() {
                    return created;
                }

                @Override
                public long getExchangeCreated() {
                    return created;
                }
            };
        }
        throw new UnsupportedInStandInsException("Extension functions " + (clazz == null ? "null" : clazz.getName()));
    }
}
