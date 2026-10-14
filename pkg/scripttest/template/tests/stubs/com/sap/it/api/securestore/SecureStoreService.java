package com.sap.it.api.securestore;

import com.sap.it.api.ITApi;
import com.sap.it.api.securestore.exception.SecureStoreException;

/** Stand-in: the secure store (in the stand-ins: test credentials from the case, never real secrets). */
public interface SecureStoreService extends ITApi {
    /** The method name is SAP's, typo included. */
    AccessTokenAndUser getAccesTokenForOauth2AuthorizationCodeCredential(String alias) throws SecureStoreException;

    UserCredential getUserCredential(String alias) throws SecureStoreException;
}
