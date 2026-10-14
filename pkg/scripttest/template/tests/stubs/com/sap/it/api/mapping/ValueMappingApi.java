package com.sap.it.api.mapping;

import com.sap.it.api.ITApi;
import java.util.List;

/** Stand-in: value mapping lookups (in the stand-ins: the case's value_mappings). */
public interface ValueMappingApi extends ITApi {
    String getMappedValue(String sourceAgency, String sourceIdentifier, String sourceValue, String targetAgency,
                          String targetIdentifier);

    List<String> getMappedValues(String sourceAgency, String sourceIdentifier, String sourceValue, String targetAgency,
                                 String targetIdentifier);
}
