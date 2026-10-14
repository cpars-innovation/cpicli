package com.sap.it.api.keystore;

import com.sap.it.api.ITApi;
import com.sap.it.api.keystore.exception.KeystoreException;
import java.security.Key;
import java.security.KeyPair;
import java.security.cert.Certificate;
import javax.net.ssl.KeyManager;
import javax.net.ssl.TrustManager;

/** Stand-in: the tenant keystore (in the stand-ins: test material from the case). */
public interface KeystoreService extends ITApi {
    Certificate getCertificate(String alias) throws KeystoreException;

    Certificate[] getCertificateChain(String alias) throws KeystoreException;

    Key getKey(String alias) throws KeystoreException;

    KeyManager getKeyManager() throws KeystoreException;

    KeyManager getKeyManager(String alias) throws KeystoreException;

    KeyManager[] getKeyManagers() throws KeystoreException;

    KeyManager[] getKeyManagers(String alias) throws KeystoreException;

    KeyPair getKeyPair(String alias) throws KeystoreException;

    TrustManager getTrustManager() throws KeystoreException;

    TrustManager[] getTrustManagers() throws KeystoreException;
}
