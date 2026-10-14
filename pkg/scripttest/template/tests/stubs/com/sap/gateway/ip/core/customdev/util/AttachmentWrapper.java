package com.sap.gateway.ip.core.customdev.util;

import javax.activation.DataHandler;
import javax.activation.DataSource;
import org.apache.camel.attachment.DefaultAttachment;

/** Stand-in: an attachment with headers, on Camel's DefaultAttachment as on the runtime. */
public class AttachmentWrapper extends DefaultAttachment {
    public AttachmentWrapper(DataHandler dh) {
        super(dh);
    }

    public AttachmentWrapper(DataSource ds) {
        super(ds);
    }
}
