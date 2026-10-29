package example.sample;

import java.util.concurrent.atomic.AtomicLong;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.cache.annotation.Cacheable;
import org.springframework.cache.annotation.EnableCaching;
import org.springframework.stereotype.Service;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@EnableCaching
public class SampleApplication {

	public static void main(String[] args) {
		SpringApplication.run(SampleApplication.class, args);
	}

	// GET /value returns how often the cached value was loaded, so a cache eviction shows as a new number.
	@RestController
	static class ValueController {

		private final ValueService service;

		ValueController(ValueService service) {
			this.service = service;
		}

		@GetMapping("/value")
		long value() {
			return service.value();
		}
	}

	@Service
	static class ValueService {

		private final AtomicLong loads = new AtomicLong();

		@Cacheable("values")
		public long value() {
			return loads.incrementAndGet();
		}
	}
}
