package com.sap.it.api.nrc;

import com.sap.it.api.ITApi;
import com.sap.it.api.nrc.exception.NumberRangeConfigException;

/** Stand-in: number ranges (in the stand-ins: each range counts from 1 per case). */
public interface NumberRangeConfigurationService extends ITApi {
    String getNextValuefromNumberRange(String name, String context) throws NumberRangeConfigException;
}
