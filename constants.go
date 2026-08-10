package posthogmcp

const (
	EventCustom            = "$mcp_custom"
	EventException         = "$exception"
	EventIdentify          = "$identify"
	EventInitialize        = "$mcp_initialize"
	EventMissingCapability = "$mcp_missing_capability"
	EventPromptGet         = "$mcp_prompt_get"
	EventPromptsList       = "$mcp_prompts_list"
	EventResourceRead      = "$mcp_resource_read"
	EventResourcesList     = "$mcp_resources_list"
	EventToolCall          = "$mcp_tool_call"
	EventToolsList         = "$mcp_tools_list"
)

const (
	PropertyClientName      = "$mcp_client_name"
	PropertyAnonDistinctID  = "$anon_distinct_id"
	PropertyClientUserAgent = "$mcp_client_user_agent"
	PropertyClientVersion   = "$mcp_client_version"
	PropertyConversationID  = "$mcp_conversation_id"
	PropertyDurationMS      = "$mcp_duration_ms"
	PropertyErrorMessage    = "$mcp_error_message"
	PropertyErrorType       = "$mcp_error_type"
	PropertyIsError         = "$mcp_is_error"
	PropertyIntent          = "$mcp_intent"
	PropertyIntentSource    = "$mcp_intent_source"
	PropertyListedToolNames = "$mcp_listed_tool_names"
	PropertyParameters      = "$mcp_parameters"
	PropertyProtocolVersion = "$mcp_protocol_version"
	PropertyResourceName    = "$mcp_resource_name"
	PropertyResponse        = "$mcp_response"
	PropertyServerName      = "$mcp_server_name"
	PropertyServerVersion   = "$mcp_server_version"
	PropertySessionID       = "$session_id"
	PropertySource          = "$mcp_source"
	PropertyToolCategory    = "$mcp_tool_category"
	PropertyToolDescription = "$mcp_tool_description"
	PropertyToolName        = "$mcp_tool_name"
	PropertyVendorClient    = "$mcp_vendor_client"
)

const (
	Source                           = "posthog_mcp_analytics"
	SDKLanguage                      = "go"
	Version                          = "0.1.0"
	DefaultMissingCapabilityToolName = "get_more_tools"
)
