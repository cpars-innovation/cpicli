package com.sap.it.api.securestore;

/** Stand-in: an OAuth2 authorization code credential's access token and user. */
public interface AccessTokenAndUser {
    String getAccessToken();

    String getUser();
}
