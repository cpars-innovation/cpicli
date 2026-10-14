package com.sap.it.script.v2.api.attachment;

import com.sap.it.script.v2.api.Attachment;
import java.util.AbstractMap;
import java.util.LinkedHashSet;
import java.util.Map;
import java.util.Set;
import javax.activation.DataHandler;

/** Stand-in: a view of attachments by their data handlers, backed by the attachment map. */
public class AttachmentMap extends AbstractMap<String, DataHandler> {
    private final Map<String, Attachment> backingMap;

    public AttachmentMap(Map<String, Attachment> backingMap) {
        this.backingMap = backingMap;
    }

    @Override
    public DataHandler put(String key, DataHandler value) {
        Attachment old = backingMap.put(key, new DefaultAttachment(value));
        return old == null ? null : old.getDataHandler();
    }

    @Override
    public Set<Map.Entry<String, DataHandler>> entrySet() {
        Set<Map.Entry<String, DataHandler>> out = new LinkedHashSet<>();
        backingMap.forEach((k, v) -> out.add(new SimpleImmutableEntry<>(k, v.getDataHandler())));
        return out;
    }

    public Map<String, Attachment> getOriginalMap() {
        return backingMap;
    }
}
