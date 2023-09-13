import threading
import ipaddress
import re 

from interfaces import mongodb_requests
from network.subnetwork_management import _addr_destringify, _addr_stringify, _addr_destringify_v6, _increase_service_address

ipv4_lock = threading.Lock()
ipv6_lock = threading.Lock()


# ....... IPv4 ....... #
########################

def new_gateway_internal_ipv4():
    """
    Function to return a new Oakestra internal IPv4 address for a registered gateway
    @return: string, a new internal IPv4 address
    """
    with ipv4_lock:
        addr = mongodb_requests.mongo_get_gateway_ip_from_cache()

        if addr is None:
            addr = mongodb_requests.mongo_get_next_gateway_ip()
            next_addr = _increase_gateway_address(addr)
            mongodb_requests.mongo_update_next_gateway_ip(next_addr)

        return _addr_stringify(addr)


def clear_gateway_ip(addr):
    """
    Function used to give back a Internal Gateway IPv4 address to the pool of available addresses
    @param addr: string,
        the address that is going to be added back to the pool
    """
    addr = _addr_destringify(addr)

    # Check if address is in the correct rage
    assert addr[1] == 252
    assert 0 <= addr[2] < 256
    assert 0 <= addr[3] < 256

    with ipv4_lock:
        next_addr = mongodb_requests.mongo_get_next_gateway_ip()

        # Ensure that the given address is actually before the next address from the pool
        assert int(str(addr[2]) + str(addr[3])) < int(str(next_addr[2]) + str(next_addr[3]))

        mongodb_requests.mongo_free_gateway_ip_to_cache(addr)


# ....... IPv6 ....... #
########################

def new_gateway_internal_ipv6():
    """
    Function to return a new Oakestra internal IPv6 address for a registered gateway
    @return: string, a new internal IPv6 address
    """
    with ipv6_lock:
        addr = mongodb_requests.mongo_get_gateway_ip_from_cache_v6()
        
        if addr is None:
            addr = mongodb_requests.mongo_get_next_gateway_ip_v6()
            next_addr = _increase_gateway_address_v6(addr)
            mongodb_requests.mongo_update_next_gateway_ip_v6(next_addr)

        return _addr_stringify(addr)
    

def clear_gateway_ip_v6(addr):
    """
    Function used to give back a Internal Gateway IPv6 address to the pool of available addresses
    @param addr: string,
        the address that is going to be added back to the pool
    """
    addr = _addr_destringify_v6(addr)

    # Check if address is in the correct rage
    assert addr[0] == 253
    assert addr[1] == 254
    for n in addr[2:10]:
        assert n == 255

    with ipv6_lock:
        next_addr = mongodb_requests.mongo_get_next_gateway_ip_v6()

        # Ensure that the give address is actually before the next address from the pool
        assert int(str(addr[10])
        + str(addr[11])
        + str(addr[12])
        + str(addr[13])
        + str(addr[14])
        + str(addr[15])
        ) < int(str(addr[10])
        + str(next_addr[11])
        + str(next_addr[12])
        + str(next_addr[13])
        + str(next_addr[14])
        + str(next_addr[15])
        )

        mongodb_requests.mongo_free_gateway_address_to_cache_v6(addr)

######### Utils

# function alias for readability
_increase_gateway_address = _increase_service_address

def _increase_gateway_address_v6(addr):
    # subnet is limited to fdfe:ffff:ffff:ffff:ffff::/64
    # convert subnet portion of addr to int and increase by one
    addr_int = int.from_bytes(addr[8:16], byteorder='big')
    addr_int += 1

    # reconvert new address part to bytearray and concatenate it with the network part of addr
    # will raise RuntimeError if address space is exhausted
    try:
        new_addr = addr_int.to_bytes(8, byteorder='big')
        new_addr = addr[0:8] + list(new_addr)
        return new_addr
    except OverflowError:
            raise RuntimeError("Exhausted Instance IP address space")