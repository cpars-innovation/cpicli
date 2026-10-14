package com.sap.gateway.ip.core.customdev.util;

import java.nio.charset.StandardCharsets;
import javax.xml.namespace.QName;
import javax.xml.stream.XMLStreamException;
import org.w3c.dom.Element;
import cpictl.standin.Bodies;

/** Stand-in: a SOAP header of the message. */
public class SoapHeader {
    private QName qname;
    private Element element;
    private boolean mustUnderstand;
    private String actor;

    public SoapHeader(QName qname, Element element, boolean mustUnderstand, String actor) {
        this.qname = qname;
        this.element = element;
        this.mustUnderstand = mustUnderstand;
        this.actor = actor;
    }

    public SoapHeader(QName qname, String xml, boolean mustUnderstand, String actor) throws XMLStreamException {
        this.qname = qname;
        try {
            this.element = Bodies.parse(xml.getBytes(StandardCharsets.UTF_8)).getDocumentElement();
        } catch (IllegalArgumentException e) {
            throw new XMLStreamException(e.getMessage());
        }
        this.mustUnderstand = mustUnderstand;
        this.actor = actor;
    }

    public QName getQname() {
        return qname;
    }

    public void setQname(QName qname) {
        this.qname = qname;
    }

    public Element getElement() {
        return element;
    }

    public void setElement(Element element) {
        this.element = element;
    }

    public boolean isMustUnderstand() {
        return mustUnderstand;
    }

    public void setMustUnderstand(boolean mustUnderstand) {
        this.mustUnderstand = mustUnderstand;
    }

    public String getActor() {
        return actor;
    }

    public void setActor(String actor) {
        this.actor = actor;
    }
}
