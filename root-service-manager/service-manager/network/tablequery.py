from interfaces import mongodb_requests


def service_resolution(name=None, ip=None, cluster_id=None):
    if cluster_id is None:
        return {}
    job = None
    if name is not None:
        job = mongodb_requests.mongo_find_job_by_name(name)
    elif ip is not None:
        job = mongodb_requests.mongo_find_job_by_ip(ip)
    if job is not None:
        return job
    return {}
