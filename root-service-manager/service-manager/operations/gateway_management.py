from interfaces import mongodb_requests

def deploy_gateway_request(node_ip=None, cluster_id=None, microservices=None):
    if (node_ip is None and cluster_id is None) or microservices is None:
        return "Invalid input arguments", 400
    
    success = mongodb_requests.mongo_gateway_add(
        node_ip = node_ip,
        cluster_id = cluster_id,
        microservices = microservices
    )
    if success is None:
        return "Error in gateway registration", 400
    return "Gateway registered: " + str(success), 200


def update_gateway_internal_addresses(gateway_id=None, internal_ipv4=None, internal_ipv6=None):
    """
        Updates gateway in database. Adds the internal Oakestra IPs assigned to the gateway.
        @params gateway_id: Gateway identification
                internal_ipv4: Oakestra internal IPv4 address 
                internal_ipv6: Oakestra internal IPv6 address
    """
    if gateway_id is None or (internal_ipv4 is None and internal_ipv6 is None):
        return "Invalid input arguments", 400
    
    mongodb_requests.mongo_find_gateway_by_id_and_update_internal_ips(
        gateway_id=gateway_id,
        internal_ipv4=internal_ipv4,
        internal_ipv6=internal_ipv6
    )
    # TODO finalize 
    return "Gateway updated", 200