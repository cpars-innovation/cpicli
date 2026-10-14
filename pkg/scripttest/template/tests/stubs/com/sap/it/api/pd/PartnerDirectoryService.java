package com.sap.it.api.pd;
public interface PartnerDirectoryService {
    <T> T getParameter(String name, String pid, Class<T> type) throws Exception;
}
