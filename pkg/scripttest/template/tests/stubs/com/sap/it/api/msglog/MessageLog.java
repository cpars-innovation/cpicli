package com.sap.it.api.msglog;

import java.util.Date;

/** Stand-in: the message processing log of the message. */
public interface MessageLog {
    void addAttachmentAsString(String name, String text, String mediaType);

    void addCustomHeaderProperty(String name, String value);

    void setBooleanProperty(String name, Boolean value);

    void setDateProperty(String name, Date value);

    void setDoubleProperty(String name, Double value);

    void setFloatProperty(String name, Float value);

    void setIntegerProperty(String name, Integer value);

    void setLongProperty(String name, Long value);

    void setStringProperty(String name, String value);
}
