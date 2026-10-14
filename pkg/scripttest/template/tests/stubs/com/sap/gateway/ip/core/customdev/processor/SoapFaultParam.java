package com.sap.gateway.ip.core.customdev.processor;

import java.util.List;
import java.util.Map;
import javax.xml.namespace.QName;
import org.w3c.dom.Element;

/** Stand-in: the parameters of a SOAP fault for {@link SoapFaultFactory#throwSoapFault(SoapFaultParam)}. */
public class SoapFaultParam {
    private String versionNamespace;
    private QName faultcode;
    private String faultstring;
    private List<QName> subcodes;
    private String faultactor;
    private Element detail;
    private Map<String, String> namespaces;

    public SoapFaultParam() {}

    public String getVersionNamespace() {
        return versionNamespace;
    }

    public void setVersionNamespace(String versionNamespace) {
        this.versionNamespace = versionNamespace;
    }

    public QName getFaultcode() {
        return faultcode;
    }

    public void setFaultcode(QName faultcode) {
        this.faultcode = faultcode;
    }

    public String getFaultstring() {
        return faultstring;
    }

    public void setFaultstring(String faultstring) {
        this.faultstring = faultstring;
    }

    public List<QName> getSubcodes() {
        return subcodes;
    }

    public void setSubcodes(List<QName> subcodes) {
        this.subcodes = subcodes;
    }

    public String getFaultactor() {
        return faultactor;
    }

    public void setFaultactor(String faultactor) {
        this.faultactor = faultactor;
    }

    public Element getDetail() {
        return detail;
    }

    public void setDetail(Element detail) {
        this.detail = detail;
    }

    public Map<String, String> getNamespaces() {
        return namespaces;
    }

    public void setNamespaces(Map<String, String> namespaces) {
        this.namespaces = namespaces;
    }
}
