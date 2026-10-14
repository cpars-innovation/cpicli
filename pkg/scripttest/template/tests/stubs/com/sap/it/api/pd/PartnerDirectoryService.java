package com.sap.it.api.pd;

import com.sap.it.api.ITApi;
import com.sap.it.api.pd.exception.PartnerDirectoryException;
import java.security.cert.X509Certificate;
import java.util.List;
import java.util.Map;

/** Stand-in: the Partner Directory (in the stand-ins: the case's pd values). */
public interface PartnerDirectoryService extends ITApi {
    String getAlternativePartnerId(String agency, String scheme, String pid) throws PartnerDirectoryException;

    List<String> getAuthorizedUsers(String pid) throws PartnerDirectoryException;

    X509Certificate getCertificateParameter(String id, String pid) throws PartnerDirectoryException;

    <T> T getParameter(String id, String pid, Class<T> type) throws PartnerDirectoryException;

    @Deprecated
    List<String> getParameterIdsMatchingPattern(String pidPattern, String idPattern, Class<?> type)
            throws PartnerDirectoryException;

    @Deprecated
    <T> Map<String, T> getParameters(String id, Class<T> type) throws PartnerDirectoryException;

    @Deprecated
    <T> Map<String, T> getParametersMatchingPattern(String pidPattern, String id, Class<T> type)
            throws PartnerDirectoryException;

    String getPartnerId(String agency, String scheme, String alternativePartnerId) throws PartnerDirectoryException;

    String getPartnerIdOfAuthorizedUser(String user) throws PartnerDirectoryException;
}
