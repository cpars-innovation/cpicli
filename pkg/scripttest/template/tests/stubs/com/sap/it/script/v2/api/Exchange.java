package com.sap.it.script.v2.api;

import java.util.Map;

/**
 * Stand-in of the version 2 script API exchange, written from SAP's published API documentation. The
 * constants carry the values Camel 3 defines for the same names.
 */
public interface Exchange {
    String AUTHENTICATION = "CamelAuthentication";
    String AUTHENTICATION_FAILURE_POLICY_ID = "CamelAuthenticationFailurePolicyId";
    @Deprecated
    String ACCEPT_CONTENT_TYPE = "CamelAcceptContentType";
    String AGGREGATED_SIZE = "CamelAggregatedSize";
    String AGGREGATED_TIMEOUT = "CamelAggregatedTimeout";
    String AGGREGATED_COMPLETED_BY = "CamelAggregatedCompletedBy";
    String AGGREGATED_CORRELATION_KEY = "CamelAggregatedCorrelationKey";
    String AGGREGATED_COLLECTION_GUARD = "CamelAggregatedCollectionGuard";
    String AGGREGATION_STRATEGY = "CamelAggregationStrategy";
    String AGGREGATION_COMPLETE_CURRENT_GROUP = "CamelAggregationCompleteCurrentGroup";
    String AGGREGATION_COMPLETE_ALL_GROUPS = "CamelAggregationCompleteAllGroups";
    String AGGREGATION_COMPLETE_ALL_GROUPS_INCLUSIVE = "CamelAggregationCompleteAllGroupsInclusive";
    String ASYNC_WAIT = "CamelAsyncWait";
    String BATCH_INDEX = "CamelBatchIndex";
    String BATCH_SIZE = "CamelBatchSize";
    String BATCH_COMPLETE = "CamelBatchComplete";
    String BEAN_METHOD_NAME = "CamelBeanMethodName";
    String BINDING = "CamelBinding";
    String BREADCRUMB_ID = "breadcrumbId";
    String CHARSET_NAME = "CamelCharsetName";
    String CIRCUIT_BREAKER_STATE = "CamelCircuitBreakerState";
    @Deprecated
    String CREATED_TIMESTAMP = "CamelCreatedTimestamp";
    String CLAIM_CHECK_REPOSITORY = "CamelClaimCheckRepository";
    String CONTENT_ENCODING = "Content-Encoding";
    String CONTENT_LENGTH = "Content-Length";
    String CONTENT_TYPE = "Content-Type";
    String COOKIE_HANDLER = "CamelCookieHandler";
    String CORRELATION_ID = "CamelCorrelationId";
    String DATASET_INDEX = "CamelDataSetIndex";
    String DEFAULT_CHARSET_PROPERTY = "org.apache.camel.default.charset";
    String DESTINATION_OVERRIDE_URL = "CamelDestinationOverrideUrl";
    String DISABLE_HTTP_STREAM_CACHE = "CamelDisableHttpStreamCache";
    String DUPLICATE_MESSAGE = "CamelDuplicateMessage";
    String DOCUMENT_BUILDER_FACTORY = "CamelDocumentBuilderFactory";
    String EXCEPTION_CAUGHT = "CamelExceptionCaught";
    String EXCEPTION_HANDLED = "CamelExceptionHandled";
    String EVALUATE_EXPRESSION_RESULT = "CamelEvaluateExpressionResult";
    String ERRORHANDLER_BRIDGE = "CamelErrorHandlerBridge";
    String ERRORHANDLER_CIRCUIT_DETECTED = "CamelErrorHandlerCircuitDetected";
    @Deprecated
    String ERRORHANDLER_HANDLED = "CamelErrorHandlerHandled";
    @Deprecated
    String EXTERNAL_REDELIVERED = "CamelExternalRedelivered";
    String FAILURE_HANDLED = "CamelFailureHandled";
    String FAILURE_ENDPOINT = "CamelFailureEndpoint";
    String FAILURE_ROUTE_ID = "CamelFailureRouteId";
    String FATAL_FALLBACK_ERROR_HANDLER = "CamelFatalFallbackErrorHandler";
    String FILE_CONTENT_TYPE = "CamelFileContentType";
    String FILE_LOCAL_WORK_PATH = "CamelFileLocalWorkPath";
    String FILE_NAME = "CamelFileName";
    String FILE_NAME_ONLY = "CamelFileNameOnly";
    String FILE_NAME_PRODUCED = "CamelFileNameProduced";
    String FILE_NAME_CONSUMED = "CamelFileNameConsumed";
    String FILE_PATH = "CamelFilePath";
    String FILE_PARENT = "CamelFileParent";
    String FILE_LAST_MODIFIED = "CamelFileLastModified";
    String FILE_LENGTH = "CamelFileLength";
    String FILE_LOCK_FILE_ACQUIRED = "CamelFileLockFileAcquired";
    String FILE_LOCK_FILE_NAME = "CamelFileLockFileName";
    String FILE_LOCK_EXCLUSIVE_LOCK = "CamelFileLockExclusiveLock";
    String FILE_LOCK_RANDOM_ACCESS_FILE = "CamelFileLockRandomAccessFile";
    String FILE_LOCK_CHANNEL_FILE = "CamelFileLockChannelFile";
    @Deprecated
    String FILTER_MATCHED = "CamelFilterMatched";
    String FILTER_NON_XML_CHARS = "CamelFilterNonXmlChars";
    String GROUPED_EXCHANGE = "CamelGroupedExchange";
    String HTTP_SCHEME = "CamelHttpScheme";
    String HTTP_HOST = "CamelHttpHost";
    String HTTP_PORT = "CamelHttpPort";
    String HTTP_BASE_URI = "CamelHttpBaseUri";
    String HTTP_CHARACTER_ENCODING = "CamelHttpCharacterEncoding";
    String HTTP_METHOD = "CamelHttpMethod";
    String HTTP_PATH = "CamelHttpPath";
    String HTTP_PROTOCOL_VERSION = "CamelHttpProtocolVersion";
    String HTTP_QUERY = "CamelHttpQuery";
    String HTTP_RAW_QUERY = "CamelHttpRawQuery";
    String HTTP_RESPONSE_CODE = "CamelHttpResponseCode";
    String HTTP_RESPONSE_TEXT = "CamelHttpResponseText";
    String HTTP_URI = "CamelHttpUri";
    String HTTP_URL = "CamelHttpUrl";
    String HTTP_CHUNKED = "CamelHttpChunked";
    String HTTP_SERVLET_REQUEST = "CamelHttpServletRequest";
    String HTTP_SERVLET_RESPONSE = "CamelHttpServletResponse";
    String INTERCEPTED_ENDPOINT = "CamelInterceptedEndpoint";
    String INTERCEPT_SEND_TO_ENDPOINT_WHEN_MATCHED = "CamelInterceptSendToEndpointWhenMatched";
    @Deprecated
    String INTERRUPTED = "CamelInterrupted";
    String LANGUAGE_SCRIPT = "CamelLanguageScript";
    String LOG_DEBUG_BODY_MAX_CHARS = "CamelLogDebugBodyMaxChars";
    String LOG_DEBUG_BODY_STREAMS = "CamelLogDebugStreams";
    String LOG_EIP_NAME = "CamelLogEipName";
    String LOOP_INDEX = "CamelLoopIndex";
    String LOOP_SIZE = "CamelLoopSize";
    String SAGA_LONG_RUNNING_ACTION = "Long-Running-Action";
    String MAXIMUM_CACHE_POOL_SIZE = "CamelMaximumCachePoolSize";
    String MAXIMUM_ENDPOINT_CACHE_SIZE = "CamelMaximumEndpointCacheSize";
    String MAXIMUM_SIMPLE_CACHE_SIZE = "CamelMaximumSimpleCacheSize";
    String MAXIMUM_TRANSFORMER_CACHE_SIZE = "CamelMaximumTransformerCacheSize";
    String MAXIMUM_VALIDATOR_CACHE_SIZE = "CamelMaximumValidatorCacheSize";
    String MESSAGE_HISTORY = "CamelMessageHistory";
    String MESSAGE_HISTORY_HEADER_FORMAT = "CamelMessageHistoryHeaderFormat";
    String MESSAGE_HISTORY_OUTPUT_FORMAT = "CamelMessageHistoryOutputFormat";
    String MESSAGE_TIMESTAMP = "CamelMessageTimestamp";
    String MULTICAST_INDEX = "CamelMulticastIndex";
    String MULTICAST_COMPLETE = "CamelMulticastComplete";
    @Deprecated
    String NOTIFY_EVENT = "CamelNotifyEvent";
    String ON_COMPLETION = "CamelOnCompletion";
    String ON_COMPLETION_ROUTE_IDS = "CamelOnCompletionRouteIds";
    String OVERRULE_FILE_NAME = "CamelOverruleFileName";
    String PARENT_UNIT_OF_WORK = "CamelParentUnitOfWork";
    String STREAM_CACHE_UNIT_OF_WORK = "CamelStreamCacheUnitOfWork";
    String RECIPIENT_LIST_ENDPOINT = "CamelRecipientListEndpoint";
    String RECEIVED_TIMESTAMP = "CamelReceivedTimestamp";
    String REDELIVERED = "CamelRedelivered";
    String REDELIVERY_COUNTER = "CamelRedeliveryCounter";
    String REDELIVERY_MAX_COUNTER = "CamelRedeliveryMaxCounter";
    @Deprecated
    String REDELIVERY_EXHAUSTED = "CamelRedeliveryExhausted";
    String REDELIVERY_DELAY = "CamelRedeliveryDelay";
    String REST_HTTP_URI = "CamelRestHttpUri";
    String REST_HTTP_QUERY = "CamelRestHttpQuery";
    @Deprecated
    String ROLLBACK_ONLY = "CamelRollbackOnly";
    @Deprecated
    String ROLLBACK_ONLY_LAST = "CamelRollbackOnlyLast";
    @Deprecated
    String ROUTE_STOP = "CamelRouteStop";
    String REUSE_SCRIPT_ENGINE = "CamelReuseScripteEngine";
    String COMPILE_SCRIPT = "CamelCompileScript";
    String SAXPARSER_FACTORY = "CamelSAXParserFactory";
    String SCHEDULER_POLLED_MESSAGES = "CamelSchedulerPolledMessages";
    String SOAP_ACTION = "CamelSoapAction";
    String SKIP_GZIP_ENCODING = "CamelSkipGzipEncoding";
    String SKIP_WWW_FORM_URLENCODED = "CamelSkipWwwFormUrlEncoding";
    String SLIP_ENDPOINT = "CamelSlipEndpoint";
    String SLIP_PRODUCER = "CamelSlipProducer";
    String SPLIT_INDEX = "CamelSplitIndex";
    String SPLIT_COMPLETE = "CamelSplitComplete";
    String SPLIT_SIZE = "CamelSplitSize";
    String STEP_ID = "CamelStepId";
    String TIMER_COUNTER = "CamelTimerCounter";
    String TIMER_FIRED_TIME = "CamelTimerFiredTime";
    String TIMER_NAME = "CamelTimerName";
    String TIMER_PERIOD = "CamelTimerPeriod";
    String TIMER_TIME = "CamelTimerTime";
    String TO_ENDPOINT = "CamelToEndpoint";
    String TRACE_EVENT = "CamelTraceEvent";
    String TRACE_EVENT_NODE_ID = "CamelTraceEventNodeId";
    String TRACE_EVENT_TIMESTAMP = "CamelTraceEventTimestamp";
    String TRACE_EVENT_EXCHANGE = "CamelTraceEventExchange";
    String TRACING_HEADER_FORMAT = "CamelTracingHeaderFormat";
    String TRACING_OUTPUT_FORMAT = "CamelTracingOutputFormat";
    String TRY_ROUTE_BLOCK = "TryRouteBlock";
    String TRANSFER_ENCODING = "Transfer-Encoding";
    String UNIT_OF_WORK_EXHAUSTED = "CamelUnitOfWorkExhausted";
    @Deprecated
    String UNIT_OF_WORK_PROCESS_SYNC = "CamelUnitOfWorkProcessSync";
    String XSLT_FILE_NAME = "CamelXsltFileName";
    String XSLT_ERROR = "CamelXsltError";
    String XSLT_FATAL_ERROR = "CamelXsltFatalError";
    String XSLT_WARNING = "CamelXsltWarning";

    Object getProperty(String name);

    @Deprecated
    Object getProperty(String name, Object defaultValue);

    void setProperty(String name, Object value);

    Object removeProperty(String name);

    Map<String, Object> getProperties();

    Map<String, Object> getAllProperties();

    boolean hasProperties();

    Message getIn();

    Message getOut();

    Message getMessage();

    Exception getException();

    void setException(Throwable t);

    CamelContext getContext();

    String getExchangeId();
}
