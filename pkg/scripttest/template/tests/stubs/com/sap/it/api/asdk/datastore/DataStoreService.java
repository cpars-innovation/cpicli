package com.sap.it.api.asdk.datastore;

import java.util.List;

/** Stand-in: the data store of the tenant (in the stand-ins: empty at the start of each case). */
public interface DataStoreService {
    MetaData put(DataBean data, DataConfig config) throws DataStoreException;

    DataBean get(String storeName, String id) throws DataStoreException;

    DataBean get(String storeName, String qualifier, String id) throws DataStoreException;

    List<DataBean> select(String storeName, int numberOfEntries) throws DataStoreException;

    List<DataBean> select(String storeName, String qualifier, int numberOfEntries) throws DataStoreException;

    boolean delete(String storeName, String id) throws DataStoreException;

    boolean delete(String storeName, String qualifier, String id) throws DataStoreException;
}
