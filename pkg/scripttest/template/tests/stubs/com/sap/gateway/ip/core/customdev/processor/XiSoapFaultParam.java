package com.sap.gateway.ip.core.customdev.processor;

import javax.xml.namespace.QName;

/** Stand-in: the parameters of an XI SOAP fault for {@link SoapFaultFactory#throwXiSoapFault(XiSoapFaultParam)}. */
public class XiSoapFaultParam {
    private String faultstring;
    private String faultactor;
    private String sapP1;
    private String sapP2;
    private String sapP3;
    private String sapP4;
    private String sapAdditionalText;
    private String sapStack;
    private QName faultcode;

    public XiSoapFaultParam() {}

    public String getFaultstring() {
        return faultstring;
    }

    public void setFaultstring(String faultstring) {
        this.faultstring = faultstring;
    }

    public String getFaultactor() {
        return faultactor;
    }

    public void setFaultactor(String faultactor) {
        this.faultactor = faultactor;
    }

    public String getSapP1() {
        return sapP1;
    }

    public void setSapP1(String sapP1) {
        this.sapP1 = sapP1;
    }

    public String getSapP2() {
        return sapP2;
    }

    public void setSapP2(String sapP2) {
        this.sapP2 = sapP2;
    }

    public String getSapP3() {
        return sapP3;
    }

    public void setSapP3(String sapP3) {
        this.sapP3 = sapP3;
    }

    public String getSapAdditionalText() {
        return sapAdditionalText;
    }

    public void setSapAdditionalText(String sapAdditionalText) {
        this.sapAdditionalText = sapAdditionalText;
    }

    public String getSapP4() {
        return sapP4;
    }

    public void setSapP4(String sapP4) {
        this.sapP4 = sapP4;
    }

    public String getSapStack() {
        return sapStack;
    }

    public void setSapStack(String sapStack) {
        this.sapStack = sapStack;
    }

    public QName getFaultcode() {
        return faultcode;
    }

    public void setFaultcode(QName faultcode) {
        this.faultcode = faultcode;
    }
}
