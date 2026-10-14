package com.sap.it.api.securestore;

import java.util.Map;

/** Stand-in: a user credential or secure parameter (the value is the password). */
public interface UserCredential {
    Map<String, String> getCredentialProperties();

    char[] getPassword();

    String getUsername();
}
