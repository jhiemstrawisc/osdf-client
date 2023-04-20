package stashcp

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

type DirectorCache struct {
	ResourceName string
	EndpointUrl  string
	Priority     int
	AuthedReq    bool
}

func HeaderParser(values string) (retMap map[string]string, err error) {
	retMap = map[string]string{}

	// Some headers might not have values
	if values == "" {
		return
	}

	mapPairs := strings.Split(values, ",")
	for _, pair := range mapPairs {
		// Remove any unwanted spaces
		pair = strings.ReplaceAll(pair, " ", "")

		// Break out key/value pairs and put in the map
		split := strings.Split(pair, "=")
		retMap[split[0]] = split[1]
	}

	return retMap, err
}

func CreateNSFromDirectorResp(dirResp *http.Response, namespace *Namespace) (err error) {

	X_OSDF_Namespace, _ := HeaderParser(dirResp.Header.Values("X-Osdf-Namespace")[0])
	namespace.Path = X_OSDF_Namespace["Namespace"]
	namespace.UseTokenOnRead, _ = strconv.ParseBool(X_OSDF_Namespace["UseTokenOnRead"])
	namespace.ReadHTTPS, _ = strconv.ParseBool(X_OSDF_Namespace["ReadHTTPS"])

	var X_OSDF_Authorization map[string]string
	if len(dirResp.Header.Values("X-Osdf-Authorization")) > 0 {
		X_OSDF_Authorization, _ = HeaderParser(dirResp.Header.Values("X-Osdf-Authorization")[0])
		namespace.Issuer = X_OSDF_Authorization["Issuer"]
	}

	// Create the caches slice
	namespace.SortedDirectorCaches, err = GetCachesFromDirectorResponse(dirResp)
	if err != nil {
		log.Errorln("Unable to construct ordered cache list:", err)
		return
	}
	log.Debugln("Namespace constructed from Director:", namespace)

	return
}

func QueryDirector(source string, directorUrl string) (resp *http.Response, err error) {
	resourceUrl := directorUrl + source

	// Prevent following the Director's redirect
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	log.Debugln("Querying OSDF Director at", resourceUrl)
	resp, err = client.Get(resourceUrl)
	log.Debugln("Director's response:", resp)

	if err != nil {
		log.Errorln("Failed to get response from OSDF Director:", err)
		return
	}

	defer resp.Body.Close()
	return

}

func GetCachesFromDirectorResponse(resp *http.Response) (caches []DirectorCache, err error) {
	// Get the Link header
	linkHeader := resp.Header.Values("Link")

	for _, linksStr := range strings.Split(linkHeader[0], ",") {
		links := strings.Split(strings.ReplaceAll(linksStr, " ", ""), ";")

		var endpoint string
		// var rel string // "rel", as defined in the Metalink/HTTP RFC. Currently not being used by
		// the OSDF Client, but is provided by the director.
		var pri int
		for _, val := range links {
			if strings.HasPrefix(val, "<") {
				endpoint = val[1 : len(val)-1]
			} else if strings.HasPrefix(val, "pri") {
				pri, _ = strconv.Atoi(val[4:])
			}
			// } else if strings.HasPrefix(val, "rel") {
			// 	rel = val[5 : len(val)-1]
			// }
		}

		// Construct the cache objects, populating only the url+port that will be used
		// based on authentication. Also, cache.Resource is currently being set as
		// the priority, because the Director at this time doesn't provide a resource
		// name. Maybe there's a way to bake that into the LINK header for each cache
		// while still following Metalink/HTTP?
		var cache DirectorCache
		port := strings.Split(endpoint, ":")[1]
		if port == "8000" {
			cache.AuthedReq = false
		} else if port == "8443" {
			cache.AuthedReq = true
		}
		// Do we need to worry about other ports?
		cache.EndpointUrl = endpoint

		cache.Priority = pri
		caches = append(caches, cache)
	}

	// Making the assumption that the Link header doesn't already provide the caches
	// in order (even though it probably does). This sorts the caches and ensures
	// we're using the "pri" tag to order them
	sort.Slice(caches, func(i, j int) bool {
		val1 := caches[i].Priority
		val2 := caches[j].Priority
		return val1 < val2
	})

	return caches, err
}

// NewTransferDetails creates the TransferDetails struct with the given cache
func NewTransferDetailsUsingDirector(cache DirectorCache, https bool) []TransferDetails {
	details := make([]TransferDetails, 0)
	cacheEndpoint := cache.EndpointUrl

	// Form the URL
	cacheURL, err := url.Parse(cacheEndpoint)
	if err != nil {
		log.Errorln("Failed to parse cache:", cache, "error:", err)
		return nil
	}
	if cacheURL.Host == "" {
		// Assume the cache is just a hostname
		cacheURL.Host = cacheEndpoint
		cacheURL.Path = ""
		cacheURL.Scheme = ""
		cacheURL.Opaque = ""
	}
	log.Debugf("Parsed Cache: %s\n", cacheURL.String())
	if https {
		cacheURL.Scheme = "https"
		if !HasPort(cacheURL.Host) {
			// Add port 8444 and 8443
			cacheURL.Host += ":8444"
			details = append(details, TransferDetails{
				Url:   *cacheURL,
				Proxy: false,
			})
			// Strip the port off and add 8443
			cacheURL.Host = cacheURL.Host[:len(cacheURL.Host)-5] + ":8443"
		}
		// Whether port is specified or not, add a transfer without proxy
		details = append(details, TransferDetails{
			Url:   *cacheURL,
			Proxy: false,
		})
	} else {
		cacheURL.Scheme = "http"
		if !HasPort(cacheURL.Host) {
			cacheURL.Host += ":8000"
		}
		isProxyEnabled := IsProxyEnabled()
		details = append(details, TransferDetails{
			Url:   *cacheURL,
			Proxy: isProxyEnabled,
		})
		if isProxyEnabled && CanDisableProxy() {
			details = append(details, TransferDetails{
				Url:   *cacheURL,
				Proxy: false,
			})
		}
	}

	return details
}
